import Foundation
import Darwin

// idlegrid-provider: provider daemon for Apple Silicon.
//
//   idlegrid-provider --coordinator ws://mbp:8090/ws/provider \
//                     --model mlx-community/Qwen2.5-0.5B-Instruct-4bit
//
// In-process MLX inference (Apple Silicon GPU via Metal): the model runs
// INSIDE this process — no subprocess, no local server, no listening port.
// Weights download from the Hugging Face Hub on first run and are cached.
// The daemon connects OUT to the coordinator over WebSocket (NAT-friendly)
// and registers only after the model is resident in memory.

struct Args {
    var coordinator = URL(string: "ws://127.0.0.1:8090/ws/provider")!
    var model = "mlx-community/Qwen2.5-0.5B-Instruct-4bit"
    var modelName = ""
    var maxTokens = 256
    var name = ""
    var code = ""
    var dryRun = false

    static func parse(_ argv: [String]) -> Args? {
        var args = Args()
        // argv[0] is the binary path; flags start at index 1.
        var i = 1
        func next(_ flag: String) -> String? {
            guard i + 1 < argv.count else {
                FileHandle.standardError.write("missing value for \(flag)\n".data(using: .utf8)!)
                return nil
            }
            i += 1
            return argv[i]
        }
        while i < argv.count {
            switch argv[i] {
            case "--coordinator":
                guard let v = next(argv[i]) else { return nil }
                args.coordinator = URL(string: v)!
            case "--model": guard let v = next(argv[i]) else { return nil }; args.model = v
            case "--model-name": guard let v = next(argv[i]) else { return nil }; args.modelName = v
            case "--max-tokens": guard let v = next(argv[i]) else { return nil }; args.maxTokens = Int(v) ?? 256
            case "--name": guard let v = next(argv[i]) else { return nil }; args.name = v
            case "--code": guard let v = next(argv[i]) else { return nil }; args.code = v
            case "--dry-run": args.dryRun = true
            case "--help", "-h":
                print(usage); return nil
            default:
                FileHandle.standardError.write("unknown flag \(argv[i])\n".data(using: .utf8)!)
                return nil
            }
            i += 1
        }
        return args
    }

    static let usage = """
    USAGE: idlegrid-provider [flags]

      --coordinator URL   coordinator WebSocket endpoint (default ws://127.0.0.1:8090/ws/provider)
      --model ID          Hugging Face model repo, MLX weights
                          (default mlx-community/Qwen2.5-0.5B-Instruct-4bit)
      --model-name NAME   model id to advertise (default: repo name)
      --max-tokens N      default generation cap when a request omits max_tokens (default 256)
      --name NAME         node name (default: local hostname)
      --code CODE         provider join code (must match coordinator's IDLEGRID_PROVIDER_CODE)
      --dry-run           register without a loaded model; requests error
    """
}

// Unbuffered stdout so logs appear when redirected to a file.
setvbuf(stdout, nil, _IONBF, 0)

let args = Args.parse(CommandLine.arguments)
guard let args else { exit(2) }

let session = URLSession(configuration: .default)

// Resolve identity.
var nodeName = args.name
if nodeName.isEmpty {
    nodeName = Host.current().localizedName ?? "mac"
}
var modelName = args.modelName
if modelName.isEmpty, !args.model.isEmpty {
    modelName = args.model.components(separatedBy: "/").last ?? args.model
}
if modelName.isEmpty { modelName = "none" }

// Privacy hardening: deny debugger attach for the process lifetime.
// Removing this protection requires disabling SIP, which requires a reboot —
// which kills this process. (Same primitive Darkbloom relies on.)
// Swift doesn't expose ptrace, so bridge it directly.
@_silgen_name("ptrace")
func c_ptrace(_ request: Int32, _ pid: Int32, _ addr: UnsafeMutableRawPointer?, _ data: Int32) -> Int32
private let PT_DENY_ATTACH: Int32 = 31
_ = c_ptrace(PT_DENY_ATTACH, 0, nil, 0)

// Load the model in-process BEFORE registering with the coordinator.
let backend: MLXBackend? = {
    if args.dryRun { return nil }
    return MLXBackend(modelID: args.model)
}()

if let backend {
    print("[mlx] loading \(args.model) into unified memory…")
    let sema = DispatchSemaphore(value: 0)
    let loadTask = Task {
        do {
            try await backend.load()
        } catch {
            FileHandle.standardError.write(
                "model load failed: \(error)\n".data(using: .utf8)!)
            exit(1)
        }
        sema.signal()
    }
    sema.wait()
    _ = loadTask // already completed or exited
}

// Graceful shutdown on SIGINT (Ctrl+C), SIGTERM, SIGHUP (terminal close).
// GOTCHA: dispatch sources MUST be retained for as long as they should fire —
// a released signal source silently stops handling signals.
var shutdownSources: [DispatchSourceSignal] = []
for sig in [SIGINT, SIGTERM, SIGHUP] {
    signal(sig, SIG_IGN)
    let source = DispatchSource.makeSignalSource(signal: sig, queue: .main)
    source.setEventHandler {
        print("[provider] shutdown signal received; exiting (in-process model freed with process)")
        exit(0)
    }
    source.resume()
    shutdownSources.append(source)
}

let client = ProviderClient(
    coordinatorURL: args.coordinator,
    name: nodeName,
    backend: backend,
    dryRun: args.dryRun,
    session: session,
    defaultMaxTokens: args.maxTokens,
    joinCode: args.code
)

print("[provider] starting; coordinator=\(args.coordinator.absoluteString) dryRun=\(args.dryRun ? "yes" : "no") backend=\(backend != nil ? "in-process MLX" : "none")")
// run() blocks forever — keep it off the main queue so signal handlers fire.
DispatchQueue.global().async {
    client.run()
}
dispatchMain()
