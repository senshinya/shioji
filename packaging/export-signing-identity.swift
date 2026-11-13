// Export exactly one selected identity; never export the entire login keychain.
import CryptoKit
import Foundation
import Security

func fail(_ message: String) -> Never {
    FileHandle.standardError.write(Data((message + "\n").utf8))
    exit(1)
}
guard CommandLine.arguments.count == 4 else { fail("Usage: export-signing-identity fingerprint password-file output.p12") }
let fingerprint = CommandLine.arguments[1].uppercased()
let password = try String(contentsOfFile: CommandLine.arguments[2], encoding: .utf8) as CFString
var found: CFTypeRef?
let query: [String: Any] = [kSecClass as String: kSecClassIdentity, kSecReturnRef as String: true, kSecMatchLimit as String: kSecMatchLimitAll]
guard SecItemCopyMatching(query as CFDictionary, &found) == errSecSuccess, let identities = found as? [SecIdentity] else { fail("No signing identity is available.") }
var selected: SecIdentity?
for identity in identities {
    var certificate: SecCertificate?
    guard SecIdentityCopyCertificate(identity, &certificate) == errSecSuccess, let certificate else { continue }
    let digest = Insecure.SHA1.hash(data: SecCertificateCopyData(certificate) as Data).map { String(format: "%02X", $0) }.joined()
    if digest == fingerprint { selected = identity; break }
}
guard let selected else { fail("The selected signing identity was not found.") }
var parameters = SecItemImportExportKeyParameters()
parameters.version = UInt32(SEC_KEY_IMPORT_EXPORT_PARAMS_VERSION)
parameters.passphrase = Unmanaged.passUnretained(password)
var exported: CFData?
let status = SecItemExport(selected, .formatPKCS12, [], &parameters, &exported)
guard status == errSecSuccess, let exported else { fail("macOS did not allow exporting the selected signing identity (status \(status)).") }
let destination = URL(fileURLWithPath: CommandLine.arguments[3])
try (exported as Data).write(to: destination, options: .atomic)
try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: destination.path)
print("Exported the selected signing identity to a private temporary file.")
