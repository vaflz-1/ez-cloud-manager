import Foundation

extension CredentialsService {
    static let appProtocolVersion = 1

    func bootstrap() throws -> AppBootstrapResponse {
        let response: AppBootstrapResponse = try decode(run(["app", "bootstrap"], input: nil))
        guard response.protocolVersion == Self.appProtocolVersion else {
            throw ServiceError.toolFailed(
                "Incompatible core protocol \(response.protocolVersion); expected \(Self.appProtocolVersion)."
            )
        }
        storeBootstrapSnapshot(response)
        return response
    }

    func listConnections(extraEnv: [String: String] = [:]) throws -> ConnectionsListResponse {
        let response: ConnectionsListResponse = try decode(
            run(["connections", "list"], input: nil, extraEnv: extraEnv)
        )
        guard response.protocolVersion == Self.appProtocolVersion else {
            throw ServiceError.toolFailed(
                "Incompatible core protocol \(response.protocolVersion); expected \(Self.appProtocolVersion)."
            )
        }
        return response
    }
}
