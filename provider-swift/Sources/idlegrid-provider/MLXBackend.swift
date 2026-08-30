import Foundation
import MLXLMCommon
import MLXLLM

/// In-process MLX inference backend — the privacy boundary.
///
/// The model runs INSIDE this hardened process via Apple's MLX (Metal):
/// no subprocess, no local server, no listening port, no IPC. The only way
/// plaintext exists on this machine is within this process's memory, which
/// the operator cannot observe without disabling SIP (a reboot, which kills
/// the process). This mirrors Darkbloom's `BackendMLXSwift` design.
final class MLXBackend: @unchecked Sendable {
    /// Hugging Face repo id, e.g. "mlx-community/Qwen2.5-0.5B-Instruct-4bit"
    let modelID: String
    /// Advertised model name on the network (last path component)
    let modelName: String

    private var container: ModelContainer?

    init(modelID: String) {
        self.modelID = modelID
        self.modelName = modelID.components(separatedBy: "/").last ?? modelID
    }

    /// Downloads weights from the Hub on first use (cached by HF HubApi)
    /// and loads the model into unified memory. Must succeed before the
    /// provider registers with the coordinator.
    func load() async throws {
        let id = modelID
        let configuration = ModelConfiguration(id: id)
        let loaded = try await LLMModelFactory.shared.loadContainer(
            configuration: configuration,
            progressHandler: { progress in
                let fraction = Int(progress.fractionCompleted * 100)
                if fraction % 20 == 0 {
                    print("[mlx] loading \(id): \(fraction)%")
                }
            }
        )
        container = loaded
        let memGB = ProcessInfo.processInfo.physicalMemory / (1024 * 1024 * 1024)
        print("[mlx] model loaded in-process: \(modelName) (\(memGB)GB machine)")
    }

    struct Outcome {
        let text: String
        let usage: UsageInfo
    }

    /// Streams one chat completion. onDelta receives incrementally
    /// detokenized text as the GPU produces it. Cancellation (from a
    /// coordinator Cancel message) stops generation at the next token.
    func generate(
        messages: [[String: String]],
        maxTokens: Int,
        temperature: Float,
        onDelta: @escaping @Sendable (String) async -> Void
    ) async throws -> Outcome {
        guard let container else { throw BackendError.notLoaded }

        let chat: [Chat.Message] = messages.compactMap { m in
            let content = m["content"] ?? ""
            switch m["role"] {
            case "system": return .system(content)
            case "assistant": return .assistant(content)
            case "user": return .user(content)
            default: return nil
            }
        }
        guard !chat.isEmpty else { throw BackendError.badRequest }

        let params = GenerateParameters(
            maxTokens: maxTokens > 0 ? maxTokens : nil,
            temperature: temperature,
            topP: 1.0
        )

        final class Box: @unchecked Sendable {
            var text = ""
            var promptTokens = 0
            var completionTokens = 0
        }
        let box = Box()

        try await container.perform { context in
            let userInput = UserInput(chat: chat)
            let input = try await context.processor.prepare(input: userInput)

            #if DEBUG_PROMPT
            let ids: [Int32] = input.text.tokens.asArray(Int32.self)
            print("[mlx] prompt token ids: \(ids.prefix(60))")
            #endif

            // Fresh cache per request: stateless serving, like a hosted API.
            let cache = context.model.newCache(parameters: params)

            for await item in try MLXLMCommon.generate(
                input: input, cache: cache, parameters: params, context: context
            ) {
                if Task.isCancelled { break }
                if let chunk = item.chunk {
                    box.text += chunk
                    await onDelta(chunk)
                }
                if let info = item.info {
                    box.promptTokens = info.promptTokenCount
                    box.completionTokens = info.generationTokenCount
                }
            }
        }

        return Outcome(
            text: box.text,
            usage: UsageInfo(
                promptTokens: box.promptTokens,
                completionTokens: box.completionTokens
            )
        )
    }

    enum BackendError: Error {
        case notLoaded
        case badRequest
    }
}
