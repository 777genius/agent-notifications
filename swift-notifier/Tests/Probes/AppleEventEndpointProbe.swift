// Standalone P2 evidence probe. No event submission, application activation,
// notification, shell dispatch or inspection of another process occurs.
import Carbon
import Foundation

var process = getpid()
var pidAddress = AEDesc()
var portAddress = AEDesc()
let created = AECreateDesc(DescType(typeKernelProcessID), &process,
                           MemoryLayout<pid_t>.size, &pidAddress)
let coerced = created == noErr
    ? AECoerceDesc(&pidAddress, DescType(typeMachPort), &portAddress)
    : created
let ownPort = AEGetRegisteredMachPort()
var serialWords: [UInt32] = [0, 0]
let serialStatus = serialWords.withUnsafeMutableBufferPointer {
    nav_get_process_serial(process, $0.baseAddress!)
}
var serial = ProcessSerialNumber(highLongOfPSN: serialWords[0], lowLongOfPSN: serialWords[1])
var serialAddress = AEDesc()
let serialDescriptorStatus = serialStatus == noErr
    ? OSStatus(AECreateDesc(DescType(typeProcessSerialNumber), &serial, MemoryLayout<ProcessSerialNumber>.size, &serialAddress))
    : serialStatus
let output: [String: Any] = [
    "processSerialLookupStatus": serialStatus,
    "processSerialDescriptorStatus": serialDescriptorStatus,
    "probe": "public_pid_to_bound_mach_port",
    "createStatus": created,
    "coerceStatus": coerced,
    "descriptorType": String(format: "%08x", portAddress.descriptorType),
    "descriptorBytes": AEGetDescDataSize(&portAddress),
    "ownPortRegistered": ownPort != MACH_PORT_NULL,
    "eventsSent": 0
]
AEDisposeDesc(&serialAddress)
AEDisposeDesc(&portAddress)
AEDisposeDesc(&pidAddress)
let data = try JSONSerialization.data(withJSONObject: output, options: [.sortedKeys])
print(String(decoding: data, as: UTF8.self))
