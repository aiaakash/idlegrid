import Foundation

// `idlegrid-provider status` — one-glance answer to "is my Mac linked and
// running?" so users never have to read raw logs.
enum StatusCommand {
    private static let label = "io.idlegrid.provider"
    private static var installRoot: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent(".idlegrid", isDirectory: true)
    }

    /// Parses KEY='value' lines from the installer's config.env.
    private static func readConfig() -> [String: String] {
        guard let raw = try? String(contentsOf: installRoot.appendingPathComponent("config.env"),
                                    encoding: .utf8) else { return [:] }
        var out: [String: String] = [:]
        for line in raw.split(separator: "\n") {
            guard let eq = line.firstIndex(of: "=") else { continue }
            let key = String(line[..<eq])
            let value = String(line[line.index(after: eq)...])
                .trimmingCharacters(in: CharacterSet(charactersIn: "'\""))
            out[key] = value
        }
        return out
    }

    private static func serviceRunning() -> Bool {
        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        p.arguments = ["print", "gui/\(getuid())/\(label)"]
        p.standardOutput = FileHandle.nullDevice
        p.standardError = FileHandle.nullDevice
        guard (try? p.run()) != nil else { return false }
        p.waitUntilExit()
        return p.terminationStatus == 0
    }

    static func run() -> Int32 {
        let config = readConfig()
        print("idlegrid provider")
        print("")

        // Account link
        if CredentialsStore.loadToken() != nil {
            print("  account:   linked (token saved at \(CredentialsStore.file.path))")
        } else {
            print("  account:   NOT linked — run: idlegrid-provider login")
        }

        // Config
        if config.isEmpty {
            print("  install:   not installed via install.sh (no ~/.idlegrid/config.env)")
        } else {
            print("  server:    \(config["IDLEGRID_SERVER"] ?? "?")")
            print("  node name: \(config["IDLEGRID_NAME"] ?? "?")")
            print("  model:     \(config["IDLEGRID_MODEL"] ?? "?")")
        }

        // Service
        print("  service:   \(serviceRunning() ? "running" : "not running")")
        print("  logs:      \(installRoot.appendingPathComponent("provider.log").path)")
        return 0
    }
}
