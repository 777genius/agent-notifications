#include "ProcessSerialBridge.h"
#include <ApplicationServices/ApplicationServices.h>
int32_t nav_get_process_serial(pid_t pid, uint32_t serial[2]) {
    ProcessSerialNumber psn = {0, 0};
    OSStatus result = GetProcessForPID(pid, &psn);
    serial[0] = psn.highLongOfPSN;
    serial[1] = psn.lowLongOfPSN;
    return result;
}
#include <Carbon/Carbon.h>
#include <string.h>
int32_t nav_send_process_serial(const uint32_t serial[2], const char *url,
                              int32_t *permission, int32_t *reply_error) {
    ProcessSerialNumber psn = {serial[0], serial[1]};
    AEAddressDesc target = {typeNull, NULL};
    AppleEvent event = {typeNull, NULL}, reply = {typeNull, NULL};
    OSStatus result = AECreateDesc(typeProcessSerialNumber, &psn, sizeof(psn), &target);
    *permission = result; *reply_error = 0;
    if (result == noErr) {
        *permission = AEDeterminePermissionToAutomateTarget(&target,
            kInternetEventClass, kAEGetURL, false);
        // Qualify the exact send without generating an Automation prompt.
        result = AECreateAppleEvent(kInternetEventClass, kAEGetURL, &target,
            kAutoGenerateReturnID, kAnyTransactionID, &event);
    }
    if (result == noErr) result = AEPutParamPtr(&event, keyDirectObject, typeUTF8Text,
                                               url, strlen(url));
    if (result == noErr) {
        result = AESendMessage(&event, &reply,
            kAEWaitReply | kAENeverInteract | kAEDoNotPromptForUserConsent, 120);
        DescType actual; Size size;
        AEGetParamPtr(&reply, keyErrorNumber, typeSInt32, &actual,
                      reply_error, sizeof(*reply_error), &size);
    }
    AEDisposeDesc(&reply); AEDisposeDesc(&event); AEDisposeDesc(&target);
    return result;
}
