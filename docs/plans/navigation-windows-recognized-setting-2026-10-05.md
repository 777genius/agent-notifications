Windows client native TEST readiness failed on source `4eae8ea11cee1321dadc6a0c7bb229082b68f0ed` ([run 37371493533](https://github.com/777genius/agent-notifications/actions/runs/37371493533)). Native C++ compilation and strict TypeScript checks passed.

The bounded loop performed 14 reads in 3000 ms. Exact AppsFolder AppID recognition first succeeded on read 10 at 2094 ms and remained true through read 14. The same notifier, created before recognition, returned `0x80070490` (Element not found) for Setting on all 14 reads. The terminal `0x80004005` is the probe's exception wrapper for budget expiry.

Show count was **0**, `showCallOutcome=not_called`, and `nativeEffectUncertain=false`. No UI invocation or cold callback occurred. Removal of the owned COM registration and shortcut succeeded. This result does not establish that Windows desktop toasts are unsupported.

A proposed diagnostic creates one fresh [ToastNotifier(exact AUMID)](https://learn.microsoft.com/en-us/uwp/api/windows.ui.notifications.toastnotificationmanager.createtoastnotifier?view=winrt-26100) after actual recognition, within the existing deadline and read limit. It records constructor and fresh Setting HRESULTs beside the old Setting HRESULT and permits Show only after fresh actual Enabled. The retained-object explanation is **unvalidated**; no fresh-factory experiment occurred in this run.

The JSON projection contains exact source, binary, uploaded ZIP and every raw artifact-file hash. Publisher ZIP digest matches the downloaded ZIP, and all ZIP members match the original downloaded artifact files. Persistent user paths and TEST identity identifiers are omitted.

Public JSON SHA256: `8f3dac049fb096f64a546d348443d345e860525878deb4f6b842c8865016c0f5`.
