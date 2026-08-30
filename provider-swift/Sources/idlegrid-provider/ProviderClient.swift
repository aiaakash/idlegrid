import Foundation

/// WebSocket client that connects out to the coordinator, registers the
/// node, heartbeats, and serves inference via the in-process MLX backend.
final class ProviderClient: @unchecked Sendable {
    private let coordinatorURL: URL
    private let name: String
    private let backend: MLXBackend?
    private let dryRun: Bool
    private let session: URLSession
    private let nodeID: String
    private let defaultMaxTokens: Int

    private var ws: URLSessionWebSocketTask?
    private let stateQueue = DispatchQueue(label: "provider.state")
    private var queueDepth = 0
    private var inflight: [String: Task<Void, Never>] = [:]
    private let joinCode: String
    private let enrollCode: String
    // Set when the coordinator permanently rejects us (e.g. bad join code):
    // stop reconnecting and exit instead.
    private var fatalRegistration = false

    // Connection lifecycle: entered on connect, left on teardown so the
    // synchronous run() loop can block until the connection actually dies.
    private let connGroup = DispatchGroup()
    private var tornDown = true

    private let heartbeatSecs: TimeInterval = 5
    private var heartbeatTimer: DispatchSourceTimer?

    init(coordinatorURL: URL, name: String, backend: MLXBackend?,
         dryRun: Bool, session: URLSession, defaultMaxTokens: Int, joinCode: String, enrollCode: String) {
        self.coordinatorURL = coordinatorURL
        self.name = name
        self.backend = backend
        self.dryRun = dryRun
        self.session = session
        self.defaultMaxTokens = defaultMaxTokens
        self.joinCode = joinCode
        self.enrollCode = enrollCode
        self.nodeID = name.lowercased().replacingOccurrences(of: " ", with: "-")
            + "-" + String(Int.random(in: 0x1000...0xffff), radix: 16)
    }

    /// Runs forever: connect, serve, reconnect with backoff.
    /// Exits (process code 1) if registration is permanently denied.
    func run() {
        var backoff: TimeInterval = 1
        while true {
            do {
                try connectAndRegister()
                backoff = 1
                receiveLoop()
                // receiveLoop only schedules callbacks; block here until
                // the connection is actually torn down.
                _ = connGroup.wait(timeout: .distantFuture)
                print("[provider] connection lost; reconnecting in \(Int(backoff))s")
            } catch {
                if stateQueue.sync(execute: { fatalRegistration }) {
                    print("[provider] fatal: giving up")
                    exit(1)
                }
                print("[provider] error: \(error.localizedDescription); retrying in \(Int(backoff))s")
            }
            cancelAllInflight(reason: "disconnected from coordinator")
            Thread.sleep(forTimeInterval: backoff)
            backoff = min(backoff * 2, 30)
        }
    }

    // MARK: - Connection

    private func connectAndRegister() throws {
        let task = session.webSocketTask(with: coordinatorURL)
        task.resume()
        ws = task
        stateQueue.sync {
            tornDown = false
            connGroup.enter()
        }

        let reg = RegisterMessage(
            nodeID: nodeID,
            name: name,
            chip: chipName(),
            memoryGB: Int(ProcessInfo.processInfo.physicalMemory / (1024 * 1024 * 1024)),
            models: [backend?.modelName ?? modelNameFallback],
            version: "v0.3.0-mlx",
            joinCode: joinCode.isEmpty ? nil : joinCode,
            enrollmentCode: enrollCode.isEmpty ? nil : enrollCode
        )
        let regEnv = try Envelope(type: MessageType.register, payload: reg)
        try send(regEnv)

        // Block until register_ok so callers don't think we're live early.
        // A register_denied is permanent: stop and exit.
        let first = try receiveOnce(types: [MessageType.registerOK, MessageType.registerDenied], timeout: 10)
        if first.type == MessageType.registerDenied {
            let denied = try? first.decode(RegisterDeniedMessage.self)
            print("[provider] REGISTRATION DENIED: \(denied?.reason ?? "unknown reason")")
            print("[provider] check the --code flag against the coordinator's IDLEGRID_PROVIDER_CODE")
            stateQueue.sync { fatalRegistration = true }
            teardownConnection()
            throw ProviderError.registerDenied
        }
        _ = try first.decode(RegisterOKMessage.self)
        print("[provider] registered as \(nodeID) (\(reg.chip), \(reg.memoryGB)GB, model=\(backend?.modelName ?? modelNameFallback))")
        startHeartbeat()
    }

    private var modelNameFallback: String { "none" }

    private func receiveOnce(expect type: String, timeout: TimeInterval) throws -> Envelope {
        try receiveOnce(types: [type], timeout: timeout)
    }

    private func receiveOnce(types accepted: [String], timeout: TimeInterval) throws -> Envelope {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            let group = DispatchGroup()
            group.enter()
            var result: Result<URLSessionWebSocketTask.Message, Error>?
            ws?.receive { msg in
                result = msg
                group.leave()
            }
            if group.wait(timeout: .now() + timeout) == .timedOut { continue }
            switch try result!.get() {
            case .string(let text):
                let env = try decodeEnvelope(text)
                if accepted.contains(env.type) { return env }
            default:
                continue
            }
        }
        throw ProviderError.registerTimeout
    }

    private func receiveLoop() {
        ws?.receive { [weak self] result in
            guard let self else { return }
            switch result {
            case .success(.string(let text)):
                self.handle(text: text)
                if self.stateQueue.sync(execute: { self.ws }) != nil { self.receiveLoop() }
            case .success:
                if self.stateQueue.sync(execute: { self.ws }) != nil { self.receiveLoop() }
            case .failure(let err):
                print("[provider] receive failed: \(err.localizedDescription)")
                self.teardownConnection()
            }
        }
    }

    private func teardownConnection() {
        let shouldLeave: Bool = stateQueue.sync {
            guard !tornDown else { return false }
            tornDown = true
            ws = nil
            return true
        }
        guard shouldLeave else { return }
        stopHeartbeat()
        connGroup.leave()
    }

    private func send(_ env: Envelope) throws {
        guard let ws = stateQueue.sync(execute: { self.ws }) else {
            throw ProviderError.notConnected
        }
        ws.send(.string(env.jsonString())) { error in
            if let error {
                print("[provider] send failed: \(error.localizedDescription)")
            }
        }
    }

    // MARK: - Heartbeat

    private func startHeartbeat() {
        stopHeartbeat()
        let timer = DispatchSource.makeTimerSource(queue: .global())
        timer.schedule(deadline: .now() + heartbeatSecs, repeating: heartbeatSecs)
        timer.setEventHandler { [weak self] in
            guard let self else { return }
            let hb = HeartbeatMessage(
                nodeID: self.nodeID,
                freeMemoryGB: self.availableMemoryGB(),
                queueDepth: self.stateQueue.sync(execute: { self.queueDepth })
            )
            if let env = try? Envelope(type: MessageType.heartbeat, payload: hb) {
                try? self.send(env)
            }
        }
        timer.resume()
        heartbeatTimer = timer
    }

    /// Available memory from Mach VM statistics (what the OS could grant us).
    private func availableMemoryGB() -> Double {
        var stats = vm_statistics64_data_t()
        var count = mach_msg_type_number_t(
            MemoryLayout<vm_statistics64_data_t>.stride / MemoryLayout<integer_t>.stride)
        let result = withUnsafeMutablePointer(to: &stats) { statsPtr in
            statsPtr.withMemoryRebound(to: integer_t.self, capacity: Int(count)) {
                host_statistics64(mach_host_self(), HOST_VM_INFO64, $0, &count)
            }
        }
        guard result == KERN_SUCCESS else {
            return Double(ProcessInfo.processInfo.physicalMemory) * 0.5 / (1024 * 1024 * 1024)
        }
        let pageSize = UInt64(vm_kernel_page_size)
        let freeBytes = UInt64(stats.free_count + stats.inactive_count + stats.purgeable_count) * pageSize
        return (Double(freeBytes) / (1024 * 1024 * 1024) * 10).rounded() / 10
    }

    private func stopHeartbeat() {
        heartbeatTimer?.cancel()
        heartbeatTimer = nil
    }

    // MARK: - Message handling

    private func handle(text: String) {
        guard let env = try? decodeEnvelope(text) else {
            print("[provider] failed to decode message: \(text.prefix(200))")
            return
        }
        switch env.type {
        case MessageType.inferenceRequest:
            guard let data = env.data,
                  let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let rid = obj["request_id"] as? String,
                  let model = obj["model"] as? String
            else {
                print("[provider] failed to decode inference_request")
                return
            }
            let stream = obj["stream"] as? Bool ?? false
            var bodyData: Data?
            if let raw = obj["body"] {
                bodyData = try? JSONSerialization.data(withJSONObject: raw)
            }
            serve(req: InferenceRequestMessage(
                requestID: rid, model: model, stream: stream, body: bodyData))
        case MessageType.cancel:
            guard let c = try? env.decode(CancelMessage.self) else { return }
            stateQueue.sync {
                inflight[c.requestID]?.cancel()
                inflight[c.requestID] = nil
            }
        default:
            break
        }
    }

    // MARK: - Inference

    private func serve(req: InferenceRequestMessage) {
        stateQueue.sync { queueDepth += 1 }

        let task = Task { [weak self] in
            guard let self else { return }
            defer { self.finish(req.requestID) }
            await self.runInference(req)
        }
        stateQueue.sync {
            inflight[req.requestID] = task
        }
    }

    private func finish(_ requestID: String) {
        stateQueue.sync {
            queueDepth = max(0, queueDepth - 1)
            inflight[requestID] = nil
        }
    }

    private func runInference(_ req: InferenceRequestMessage) async {
        let accepted = try? Envelope(
            type: MessageType.inferenceAccepted,
            payload: InferenceAcceptedMessage(requestID: req.requestID)
        )
        if let accepted { try? send(accepted) }

        guard let backend, !dryRun else {
            sendError(req.requestID, "dry-run provider: no backend loaded")
            return
        }

        do {
            guard let body = req.body,
                  let bodyDict = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
                  let rawMessages = bodyDict["messages"] as? [[String: Any]]
            else {
                sendError(req.requestID, "malformed request body")
                return
            }
            let messages: [[String: String]] = rawMessages.compactMap { m in
                guard let role = m["role"] as? String,
                      let content = m["content"] as? String else { return nil }
                return ["role": role, "content": content]
            }
            guard !messages.isEmpty else {
                sendError(req.requestID, "no usable messages in request")
                return
            }

            let maxTokens = (bodyDict["max_tokens"] as? Int)
                ?? (bodyDict["max_tokens"] as? NSNumber)?.intValue
                ?? defaultMaxTokens
            let temperature = (bodyDict["temperature"] as? NSNumber)?.floatValue ?? 0.7

            let outcome = try await backend.generate(
                messages: messages,
                maxTokens: maxTokens,
                temperature: temperature,
                onDelta: { [weak self] delta in
                    guard let self else { return }
                    if Task.isCancelled { return }
                    let chunk = try? Envelope(
                        type: MessageType.inferenceChunk,
                        payload: InferenceChunkMessage(requestID: req.requestID, delta: delta)
                    )
                    if let chunk { try? self.send(chunk) }
                }
            )

            let completion = try Envelope(
                type: MessageType.inferenceComplete,
                payload: InferenceCompleteMessage(
                    requestID: req.requestID,
                    usage: UsageInfo(
                        promptTokens: outcome.usage.promptTokens,
                        completionTokens: outcome.usage.completionTokens
                    )
                )
            )
            try send(completion)
            print("[provider] req \(req.requestID.prefix(12))… done: \(outcome.usage.promptTokens) in / \(outcome.usage.completionTokens) out tokens")
        } catch is CancellationError {
            sendError(req.requestID, "cancelled")
        } catch {
            sendError(req.requestID, "inference failed: \(error.localizedDescription)")
        }
    }

    private func sendError(_ requestID: String, _ message: String) {
        let env = try? Envelope(
            type: MessageType.inferenceError,
            payload: InferenceErrorMessage(requestID: requestID, error: message)
        )
        if let env { try? send(env) }
    }

    private func cancelAllInflight(reason: String) {
        let tasks = stateQueue.sync { () -> [Task<Void, Never>] in
            let all = Array(inflight.values)
            inflight.removeAll()
            return all
        }
        guard !tasks.isEmpty else { return }
        print("[provider] failing \(tasks.count) inflight requests: \(reason)")
        tasks.forEach { $0.cancel() }
    }
}

enum ProviderError: Error {
    case notConnected
    case registerTimeout
    case registerDenied
    case badMessage
}

private func decodeEnvelope(_ text: String) throws -> Envelope {
    // The wire format has "data" as a raw JSON value, but Swift's Codable
    // treats Data fields as base64 strings — so parse by hand.
    guard let obj = try JSONSerialization.jsonObject(with: Data(text.utf8)) as? [String: Any],
          let type = obj["type"] as? String
    else { throw ProviderError.badMessage }
    var data: Data?
    if let raw = obj["data"] {
        data = try JSONSerialization.data(withJSONObject: raw)
    }
    return Envelope(type: type, data: data)
}

func chipName() -> String {
    var size = 0
    sysctlbyname("machdep.cpu.brand_string", nil, &size, nil, 0)
    guard size > 0 else { return "unknown" }
    var name = [CChar](repeating: 0, count: size)
    sysctlbyname("machdep.cpu.brand_string", &name, &size, nil, 0)
    return String(cString: name)
}
