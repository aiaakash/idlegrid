import Foundation

// Wire protocol — must stay in sync with idlegrid/protocol/messages.go.

enum MessageType {
    static let register = "register"
    static let registerOK = "register_ok"
    static let registerDenied = "register_denied"
    static let heartbeat = "heartbeat"
    static let inferenceRequest = "inference_request"
    static let inferenceAccepted = "inference_accepted"
    static let inferenceChunk = "inference_chunk"
    static let inferenceComplete = "inference_complete"
    static let inferenceError = "inference_error"
    static let cancel = "cancel"
}

// Envelope is the outer frame for every WebSocket message. Note: `data`
// holds raw JSON bytes (not Codable — see decodeEnvelope in ProviderClient).
struct Envelope {
    let type: String
    let data: Data?

    private enum CodingKeys: String, CodingKey { case type, data }

    init(type: String, data: Data? = nil) {
        self.type = type
        self.data = data
    }

    init<T: Encodable>(type: String, payload: T) throws {
        self.type = type
        self.data = try JSONEncoder().encode(payload)
    }

    func decode<T: Decodable>(_ as: T.Type) throws -> T {
        try JSONDecoder().decode(T.self, from: data ?? Data("{}".utf8))
    }

    func jsonString() -> String {
        if let data, let s = String(data: data, encoding: .utf8) {
            return "{\"type\":\"\(type)\",\"data\":\(s)}"
        }
        return "{\"type\":\"\(type)\"}"
    }
}

struct RegisterMessage: Codable {
    var nodeID: String
    var name: String
    var chip: String
    var memoryGB: Int
    var models: [String]
    var version: String
    var joinCode: String?

    private enum CodingKeys: String, CodingKey {
        case nodeID = "node_id"
        case name, chip
        case memoryGB = "memory_gb"
        case models, version
        case joinCode = "join_code"
    }
}

struct RegisterDeniedMessage: Codable {
    var reason: String
    private enum CodingKeys: String, CodingKey {
        case reason
    }
}

struct RegisterOKMessage: Codable {
    var heartbeatIntervalSecs: Int
    private enum CodingKeys: String, CodingKey {
        case heartbeatIntervalSecs = "heartbeat_interval_secs"
    }
}

struct HeartbeatMessage: Codable {
    var nodeID: String
    var freeMemoryGB: Double
    var queueDepth: Int

    private enum CodingKeys: String, CodingKey {
        case nodeID = "node_id"
        case freeMemoryGB = "free_memory_gb"
        case queueDepth = "queue_depth"
    }
}

struct InferenceRequestMessage: Codable {
    var requestID: String
    var model: String
    var stream: Bool
    var body: Data?

    private enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
        case model, stream, body
    }
}

struct InferenceAcceptedMessage: Codable {
    var requestID: String
    private enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
    }
}

struct InferenceChunkMessage: Codable {
    var requestID: String
    var delta: String
    private enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
        case delta
    }
}

struct UsageInfo: Codable {
    var promptTokens: Int
    var completionTokens: Int

    private enum CodingKeys: String, CodingKey {
        case promptTokens = "prompt_tokens"
        case completionTokens = "completion_tokens"
    }
}

struct InferenceCompleteMessage: Codable {
    var requestID: String
    var usage: UsageInfo
    private enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
        case usage
    }
}

struct InferenceErrorMessage: Codable {
    var requestID: String
    var error: String
    private enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
        case error
    }
}

struct CancelMessage: Codable {
    var requestID: String
    private enum CodingKeys: String, CodingKey {
        case requestID = "request_id"
    }
}
