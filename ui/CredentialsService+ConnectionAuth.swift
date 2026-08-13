import Foundation

extension CredentialsService {
    static let connectionAuthProtocolVersion = 1

    func discoverConnectionAuth(
        provider: String,
        extraEnv: [String: String] = [:],
        cancellation: FastProcessCancellation? = nil
    ) throws -> ConnectionAuthSnapshot {
        let response: ConnectionAuthSnapshot = try decode(run(
            ["connections", "auth", "discover", "--provider", provider],
            inputData: nil,
            extraEnv: extraEnv,
            cancellation: cancellation
        ))
        try validateConnectionAuthSnapshot(response, provider: provider)
        return response
    }

    func loginConnectionAuth(
        provider: String,
        request: ConnectionAuthLoginRequest,
        extraEnv: [String: String] = [:],
        cancellation: FastProcessCancellation? = nil
    ) throws -> ConnectionAuthLoginResponse {
        let payload = try JSONEncoder().encode(request)
        let response: ConnectionAuthLoginResponse = try decode(run(
            ["connections", "auth", "login", "--provider", provider],
            inputData: payload,
            extraEnv: extraEnv,
            cancellation: cancellation
        ))
        guard response.provider == provider else {
            throw ServiceError.toolFailed("Connection auth provider mismatch.")
        }
        guard response.ok else {
            throw ServiceError.toolFailed("The provider did not complete sign-in.")
        }
        if let snapshot = response.snapshot {
            try validateConnectionAuthSnapshot(snapshot, provider: provider)
        }
        return response
    }

    func applyConnectionAuth(
        provider: String,
        request: ConnectionAuthApplyRequest,
        extraEnv: [String: String] = [:],
        cancellation: FastProcessCancellation? = nil
    ) throws -> ConnectionAuthApplyResponse {
        let payload = try JSONEncoder().encode(request)
        let response: ConnectionAuthApplyResponse = try decode(run(
            ["connections", "auth", "apply", "--provider", provider],
            inputData: payload,
            extraEnv: extraEnv,
            cancellation: cancellation
        ))
        guard response.provider == provider else {
            throw ServiceError.toolFailed("Connection sync provider mismatch.")
        }
        return response
    }

    private func validateConnectionAuthSnapshot(
        _ snapshot: ConnectionAuthSnapshot,
        provider: String
    ) throws {
        guard snapshot.protocolVersion == Self.connectionAuthProtocolVersion else {
            throw ServiceError.toolFailed(
                "Incompatible connection auth protocol \(snapshot.protocolVersion); expected \(Self.connectionAuthProtocolVersion)."
            )
        }
        guard snapshot.provider == provider else {
            throw ServiceError.toolFailed("Connection auth provider mismatch.")
        }
    }
}
