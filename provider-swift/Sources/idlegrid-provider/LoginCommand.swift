import Foundation

// `idlegrid-provider login` — RFC 8628-style device authorization.
// The CLI requests a device code from the coordinator, the user approves it
// in the console (their email session proves identity), and the CLI polls
// until it receives a provider token bound to that account. The token is
// stored at ~/.config/idlegrid/credentials.json and presented as auth_token
// on every WS register — replacing the static --enroll-code.

enum CredentialsStore {
    static var dir: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".config/idlegrid", isDirectory: true)
    }
    static var file: URL { dir.appendingPathComponent("credentials.json") }

    static func save(token: String) throws {
        let fm = FileManager.default
        try fm.createDirectory(at: dir, withIntermediateDirectories: true,
                               attributes: [.posixPermissions: 0o700])
        let body = try JSONEncoder().encode(["provider_token": token])
        try body.write(to: file, options: .atomic)
        try fm.setAttributes([.posixPermissions: 0o600], ofItemAtPath: file.path)
    }

    static func loadToken() -> String? {
        guard let data = try? Data(contentsOf: file),
              let obj = try? JSONDecoder().decode([String: String].self, from: data),
              let token = obj["provider_token"], !token.isEmpty else { return nil }
        return token
    }
}

enum LoginCommand {
    private struct DeviceCodeResponse: Decodable {
        let deviceCode: String
        let userCode: String
        let verificationURL: String
        let expiresIn: Int
        let interval: Int

        private enum CodingKeys: String, CodingKey {
            case deviceCode = "device_code"
            case userCode = "user_code"
            case verificationURL = "verification_url"
            case expiresIn = "expires_in"
            case interval
        }
    }

    private struct DeviceTokenResponse: Decodable {
        let providerToken: String?
        let error: String?

        private enum CodingKeys: String, CodingKey {
            case providerToken = "provider_token"
            case error
        }
    }

    /// Synchronous POST of a JSON body; returns (HTTP status, decoded payload).
    private static func post<T: Decodable>(_ url: URL, json: [String: String]) throws -> (Int, T) {
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(json)
        var result: (Int, Data)?
        var reqError: Error?
        let sema = DispatchSemaphore(value: 0)
        URLSession.shared.dataTask(with: req) { data, res, err in
            if let data, let http = res as? HTTPURLResponse {
                result = (http.statusCode, data)
            } else {
                reqError = err ?? URLError(.unknown)
            }
            sema.signal()
        }.resume()
        sema.wait()
        guard let (status, data) = result else { throw reqError ?? URLError(.unknown) }
        return (status, try JSONDecoder().decode(T.self, from: data))
    }

    /// If the provider LaunchAgent is already installed, restart it so the
    /// running daemon picks up the new token immediately — no manual
    /// `launchctl kickstart` step for the user.
    private static func restartServiceIfInstalled() {
        let plist = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/LaunchAgents/io.idlegrid.provider.plist")
        guard FileManager.default.fileExists(atPath: plist.path) else { return }
        let kick = Process()
        kick.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        kick.arguments = ["kickstart", "-k", "gui/\(getuid())/io.idlegrid.provider"]
        kick.standardOutput = FileHandle.nullDevice
        kick.standardError = FileHandle.nullDevice
        if (try? kick.run()) != nil {
            kick.waitUntilExit()
            if kick.terminationStatus == 0 {
                print("  ✓ Provider service restarted — it is now enrolled")
            }
        }
    }

    /// ws(s)://host/ws/provider -> http(s)://host
    static func httpBase(from coordinator: URL) -> URL {
        var c = URLComponents(url: coordinator, resolvingAgainstBaseURL: false)!
        c.scheme = (c.scheme == "wss") ? "https" : "http"
        c.path = ""
        c.query = nil
        return c.url!
    }

    /// args: flags after the `login` subcommand. Returns the process exit code.
    static func run(args: [String]) -> Int32 {
        var coordinator = defaultCoordinatorURL
        var i = 0
        while i < args.count {
            if args[i] == "--coordinator", i + 1 < args.count, let u = URL(string: args[i + 1]) {
                coordinator = u
                i += 2
            } else {
                FileHandle.standardError.write("usage: idlegrid-provider login [--coordinator ws(s)://host/ws/provider]\n".data(using: .utf8)!)
                return 2
            }
        }
        let base = httpBase(from: coordinator)

        let dc: DeviceCodeResponse
        do {
            let (status, res): (Int, DeviceCodeResponse) = try post(
                base.appendingPathComponent("/v1/device/code"), json: [:])
            guard status == 200 else {
                FileHandle.standardError.write("login failed: coordinator returned HTTP \(status)\n".data(using: .utf8)!)
                return 1
            }
            dc = res
        } catch {
            FileHandle.standardError.write("login failed: \(error.localizedDescription)\n".data(using: .utf8)!)
            return 1
        }

        let linkURL = "\(dc.verificationURL)?code=\(dc.userCode)"
        print("")
        print("  To link this Mac to your account:")
        print("")
        print("    1. Open  \(linkURL)")
        print("    2. Enter code  \(dc.userCode)")
        print("")
        print("  Waiting for approval (expires in \(dc.expiresIn / 60) min)…")
        // Best-effort: open the browser for the user.
        let opener = Process()
        opener.executableURL = URL(fileURLWithPath: "/usr/bin/open")
        opener.arguments = [linkURL]
        try? opener.run()

        let deadline = Date().addingTimeInterval(TimeInterval(dc.expiresIn))
        let pollEvery = max(TimeInterval(dc.interval), 2)
        while Date() < deadline {
            Thread.sleep(forTimeInterval: pollEvery)
            do {
                let (status, res): (Int, DeviceTokenResponse) = try post(
                    base.appendingPathComponent("/v1/device/token"),
                    json: ["device_code": dc.deviceCode])
                if status == 200, let token = res.providerToken {
                    try CredentialsStore.save(token: token)
                    print("  ✓ Linked. Token saved to \(CredentialsStore.file.path)")
                    restartServiceIfInstalled()
                    print("  Start serving with: idlegrid-provider --coordinator \(coordinator.absoluteString) …")
                    return 0
                }
                switch res.error {
                case "authorization_pending", "slow_down":
                    continue // keep polling
                case "expired_token":
                    FileHandle.standardError.write("code expired — run login again\n".data(using: .utf8)!)
                    return 1
                default:
                    FileHandle.standardError.write("login failed: \(res.error ?? "HTTP \(status)")\n".data(using: .utf8)!)
                    return 1
                }
            } catch {
                FileHandle.standardError.write("poll error: \(error.localizedDescription) — retrying\n".data(using: .utf8)!)
            }
        }
        FileHandle.standardError.write("timed out waiting for approval — run login again\n".data(using: .utf8)!)
        return 1
    }
}
