// Live-process signature observation only. Never grants native admission.
import Foundation
import Security

guard CommandLine.arguments.count == 2, let pid = Int32(CommandLine.arguments[1]), pid > 0 else {
    exit(2)
}
let attributes = [kSecGuestAttributePid as String: NSNumber(value: pid)] as CFDictionary
var code: SecCode?
guard SecCodeCopyGuestWithAttributes(nil, attributes, SecCSFlags(), &code) == errSecSuccess,
      let code else { exit(3) }

func check(team: String) -> OSStatus {
    let expression = "anchor apple generic and identifier \"opencode\" and certificate leaf[subject.OU] = \"\(team)\"" as CFString
    var requirement: SecRequirement?
    guard SecRequirementCreateWithString(expression, SecCSFlags(), &requirement) == errSecSuccess,
          let requirement else { return errSecParam }
    return SecCodeCheckValidity(code, SecCSFlags(rawValue: kSecCSStrictValidate), requirement)
}
let actual = check(team: "5NZ4Q7NXJ4")
let wrong = check(team: "0000000000")
let output = ["publisherMatches": actual == errSecSuccess,
              "wrongTeamRefused": wrong == errSecCSReqFailed]
guard let data = try? JSONSerialization.data(withJSONObject: output, options: [.sortedKeys]),
      let text = String(data: data, encoding: .utf8) else { exit(4) }
print(text)
