"""Goal17 SAME R340 installed USER carrier, under construction; NOT_RUN here.
Source-bound outer observer/session, two planned phases and public readback bridge.
Native/OS/E NOT_RUN. Root build qualification and exact independent review apply.
"""
import argparse, ast, base64, ctypes, datetime, gzip, hashlib, http.server, io, json, os, pathlib, re, copy, unicodedata
import platform, resource, shlex, signal, socket, stat, struct, subprocess, sys, threading, time, types, zipfile, tarfile, fcntl, select, termios
HERE = pathlib.Path(__file__).resolve().parent
SOURCE_SHA = '9f014e72af510783d58d00ad3d2db455a2f8117b'
WIRE_SHA = 'c0033c7b5a01b252815e0c9bf0ea47062150a03bc857b71d4a2e9317127b6ea2'
INDEX_SHA = '45d9b1df85d0165cb2e690f96fa5fbe4b59e8a71ddd22176a301ba7e5b18a0b9'
LAUNCHER_SHA = '2ccc9a8e167797641448b5e5c936f006ba137a2555f117f38c5eb76a5238a233'
CURSOR_ARCHIVE_SHA = '6e4cd936a4866b8a77c50ff51a564460d715772fabc477a01aa0f0455d9559f0'
CONSENT = 'TEST-only-public-sdk-stop-and-workspace-trust'
LIMIT = 1048576
TEST_RUNTIME_FILE_LIMIT = 25264137
REFUSALS = ['/aiserver.v1.DashboardService/ListMarketplaces',
            '/aiserver.v1.DashboardService/GetCliDownloadUrl',
            '/aiserver.v1.DashboardService/GetGlobalCommands', '/aiserver.v1.AnalyticsService/SubmitLogs']
w = None
CASE_CLOCK = None
SESSION_ENV = {}
READBACK_INPUTS = None
AUTHORITY_BASELINE = {}
READBACK_SOURCE_SHA256 = '77030250348f8af0ee99878627b59c39cc7d658974c11adca67f7d98a4d4c8fc'
READBACK_SOURCE_BYTES = base64.b64decode('Ly8gUmVhZC1vbmx5IGJyaWRnZSB0byB0aGUgU0FNRSBwaW5uZWQgcHVibGljIEFQSXMuIFJvb3QgYnVpbGRzIHRoaXMgcGFja2V0IGZpbGUKLy8gaW4gdGhlIGV4YWN0IHNvdXJjZSBtb2R1bGU7IG5vIHByb2R1Y3QgY29tbWFuZCwgaG9vaywgY2FsbGJhY2sgb3IgQUNLIGlzIGFkZGVkLgovLyBUaGlzIHdvcmtlciBkb2VzIG5vdCBidWlsZCBvciBleGVjdXRlIGl0LiBBbGwgbXV0YXRpb25zIHN0YXkgd2l0aCB0aGUgd2l6YXJkLgpwYWNrYWdlIG1haW4KaW1wb3J0ICgKImJ5dGVzIgoiY29udGV4dCIKImNyeXB0by9zaGEyNTYiCiJlbmNvZGluZy9qc29uIgoiZXJyb3JzIgoiZm10IgoiaW8iCiJvcyIKInBhdGgvZmlsZXBhdGgiCiJydW50aW1lL2RlYnVnIgoidGltZSIKImdpdGh1Yi5jb20vNzc3Z2VuaXVzL2FnZW50LW5vdGlmaWNhdGlvbnMvaW50ZXJuYWwvYWdlbnRub3RpZnkvcG9ydGFibGUiCiJnaXRodWIuY29tLzc3N2dlbml1cy9hZ2VudC1ub3RpZmljYXRpb25zL2ludGVybmFsL2NvcGlsb3R2c2NvZGVpbnN0YWxsIgoiZ2l0aHViLmNvbS83NzdnZW5pdXMvYWdlbnQtbm90aWZpY2F0aW9ucy9pbnRlcm5hbC9jdXJzb3JpbnN0YWxsIgoiZ2l0aHViLmNvbS83NzdnZW5pdXMvYWdlbnQtbm90aWZpY2F0aW9ucy9pbnRlcm5hbC9pbnN0YWxscnVudGltZSIKImdpdGh1Yi5jb20vNzc3Z2VuaXVzL3BsdWdpbi1raXQtYWkvaW5zdGFsbC9pbnRlZ3JhdGlvbmN0bC9hZGFwdGVycy9wYXRocG9saWN5IgoiZ2l0aHViLmNvbS83NzdnZW5pdXMvcGx1Z2luLWtpdC1haS9pbnN0YWxsL2ludGVncmF0aW9uY3RsL2FnZW50cGx1Z2lucy9hZGFwdGVycy9uYXRpdmVjb25maWciCiJnaXRodWIuY29tLzc3N2dlbml1cy9wbHVnaW4ta2l0LWFpL2luc3RhbGwvaW50ZWdyYXRpb25jdGwvYWdlbnRwbHVnaW5zL2FkYXB0ZXJzL3Byb2ZpbGVhdXRob3JpdHkiCiJnaXRodWIuY29tLzc3N2dlbml1cy9wbHVnaW4ta2l0LWFpL2luc3RhbGwvaW50ZWdyYXRpb25jdGwvYWdlbnRwbHVnaW5zL2FkYXB0ZXJzL3N0YXRldjIiCiJnaXRodWIuY29tLzc3N2dlbml1cy9wbHVnaW4ta2l0LWFpL2luc3RhbGwvaW50ZWdyYXRpb25jdGwvYWdlbnRwbHVnaW5zL2NsaWVudHMiCiJnaXRodWIuY29tLzc3N2dlbml1cy9wbHVnaW4ta2l0LWFpL2luc3RhbGwvaW50ZWdyYXRpb25jdGwvYWdlbnRwbHVnaW5zL2N1cnNvcmhvb2tzIgoiZ2l0aHViLmNvbS83NzdnZW5pdXMvcGx1Z2luLWtpdC1haS9pbnN0YWxsL2ludGVncmF0aW9uY3RsL2FnZW50cGx1Z2lucy9kb21haW4iCnVhcCAiZ2l0aHViLmNvbS83NzdnZW5pdXMvcGx1Z2luLWtpdC1haS9pbnN0YWxsL2ludGVncmF0aW9uY3RsL2FnZW50cGx1Z2lucy9pbnN0YWxsZXIiCikKLy8gUmVxdWlyZWQgbGRmbGFnOyBoYXNoZWQgZXhhY3QgYnJpZGdlIHNvdXJjZSBpcyBpbmRlcGVuZGVudGx5IGJvdW5kIGJ5IGNhcnJpZXIuCnZhciBwYWNrZXRTb3VyY2VTSEEyNTYgc3RyaW5nCmNvbnN0IHByb2R1Y3RSZXZpc2lvbiA9ICI5ZjAxNGU3MmFmNTEwNzgzZDU4ZDAwYWQzZDJkYjQ1NWEyZjgxMTdiIgp0eXBlIHJlcXVlc3Qgc3RydWN0IHsKCU1vZGUgc3RyaW5nIGBqc29uOiJtb2RlImAKCVNlbGVjdG9yIHN0cmluZyBganNvbjoic2VsZWN0b3IiYAoJQmluZGluZyBwb3J0YWJsZS5CaW5kaW5nIGBqc29uOiJiaW5kaW5nImAKCU9yaWdpbmFsQXV0aG9yaXR5ICpkb21haW4uUHJvZmlsZUF1dGhvcml0eSBganNvbjoib3JpZ2luYWxBdXRob3JpdHkiYAoJT3JpZ2luYWxOYW1lc3BhY2Ugc3RyaW5nIGBqc29uOiJvcmlnaW5hbE5hbWVzcGFjZSJgCglGaXhlZCAqY3Vyc29yaW5zdGFsbC5BdXRob3JpdHkgYGpzb246ImZpeGVkImAKCVRlbXBSb290IHN0cmluZyBganNvbjoidGVtcFJvb3QiYAoJVGltZW91dE1pbGxpcyBpbnQgYGpzb246InRpbWVvdXRNaWxsaXMiYAoJU291cmNlU0hBMjU2IHN0cmluZyBganNvbjoic291cmNlU0hBMjU2ImAKfQpmdW5jIHJlcXVpcmUob2sgYm9vbCwgbWVzc2FnZSBzdHJpbmcpIGVycm9yIHsKCWlmICFvayB7CgkJcmV0dXJuIGVycm9ycy5OZXcobWVzc2FnZSkKCX07CglyZXR1cm4gbmlsCn0KZnVuYyByZWFkYmFjayhyIHJlcXVlc3QpIChtYXBbc3RyaW5nXWFueSxlcnJvcikgewoJaWYgZXJyIDo9IHJlcXVpcmUoci5Tb3VyY2VTSEEyNTYgIT0gIiIgJiYgci5Tb3VyY2VTSEEyNTYgPT0gcGFja2V0U291cmNlU0hBMjU2ICYmIHIuVGltZW91dE1pbGxpcyA+IDAgJiYgci5UaW1lb3V0TWlsbGlzIDw9IDUwMDAsICJwYWNrZXQvZGVhZGxpbmUgYmluZGluZyIpOwoJZXJyICE9IG5pbCB7CgkJcmV0dXJuIG5pbCxlcnIKCX0KCWluZm8sb2s6PWRlYnVnLlJlYWRCdWlsZEluZm8oKTsKCWlmICFvayB7CgkJcmV0dXJuIG5pbCxlcnJvcnMuTmV3KCJidWlsZCBwcm92ZW5hbmNlIG1pc3NpbmciKQoJfQoJcmV2aXNpb24sbW9kaWZpZWQ6PSIiLCIiCglmb3IgXyxzOj1yYW5nZSBpbmZvLlNldHRpbmdzIHsKCQlpZiBzLktleT09InZjcy5yZXZpc2lvbiIgewoJCQlyZXZpc2lvbj1zLlZhbHVlCgkJfTsKCQlpZiBzLktleT09InZjcy5tb2RpZmllZCIgewoJCQltb2RpZmllZD1zLlZhbHVlCgkJfX0KCWlmIGVycjo9cmVxdWlyZShyZXZpc2lvbj09cHJvZHVjdFJldmlzaW9uICYmIG1vZGlmaWVkPT0iZmFsc2UiLCAiZXhhY3QgcHVibGljIGJ1aWxkIHJldmlzaW9uIik7CgllcnIhPW5pbCB7CgkJcmV0dXJuIG5pbCxlcnIKCX0KCWN0eCxjYW5jZWw6PWNvbnRleHQuV2l0aFRpbWVvdXQoY29udGV4dC5CYWNrZ3JvdW5kKCksdGltZS5EdXJhdGlvbihyLlRpbWVvdXRNaWxsaXMpKnRpbWUuTWlsbGlzZWNvbmQpOwoJZGVmZXIgY2FuY2VsKCkKCWI6PXIuQmluZGluZwoJaWYgci5Nb2RlIT0iYWJzZW50IiB7CgkJbGl2ZSxlcnI6PXBvcnRhYmxlLlJlYWRDdXJzb3JCaW5kaW5nKHIuU2VsZWN0b3IpOwoJCWlmIGVyciE9bmlsIHsKCQkJcmV0dXJuIG5pbCxlcnIKCQl9CgkJaWYgYi5WZXJzaW9uIT0wICYmIGxpdmUhPWIgewoJCQlyZXR1cm4gbmlsLGVycm9ycy5OZXcoIm9yaWdpbmFsIHBvcnRhYmxlIGJpbmRpbmcgY2hhbmdlZCIpCgkJfTsKCQliPWxpdmUKCX0KCWlmIGVycjo9cmVxdWlyZShiLkludGVncmF0aW9uPT1wb3J0YWJsZS5DdXJzb3IgJiYgZmlsZXBhdGguSXNBYnMoci5UZW1wUm9vdCkgJiYgZmlsZXBhdGguQ2xlYW4oci5UZW1wUm9vdCk9PXIuVGVtcFJvb3QsImV4cGxpY2l0IEN1cnNvciBsYXlvdXQiKTsKCWVyciE9bmlsIHsKCQlyZXR1cm4gbmlsLGVycgoJfQoJc25hcCxlcnI6PWluc3RhbGxydW50aW1lLlJlYWRJbnN0YWxsZWRTbmFwc2hvdChiLkNvbnRyb2xSb290KTsKCWlmIGVyciE9bmlsIHsKCQlyZXR1cm4gbmlsLGVycgoJfQoJaWYgZXJyPXJlcXVpcmUoIXNuYXAuUmVjb3ZlcnkgJiYgc25hcC5MZWRnZXIuUGVuZGluZ011dGF0aW9uPT1uaWwsInBlbmRpbmcgY29yZSBtdXRhdGlvbi9yZWNvdmVyeSIpOwoJZXJyIT1uaWwgewoJCXJldHVybiBuaWwsZXJyCgl9Cglyb290Oj1maWxlcGF0aC5Kb2luKGZpbGVwYXRoLkRpcihiLkNvbnRyb2xSb290KSwidWFwIikKCWNmZzo9dWFwLkNvbmZpZ3tTdGF0ZVJvb3Q6ZmlsZXBhdGguSm9pbihyb290LCJzdGF0ZSIpLFN0YXRlRmlsZTpmaWxlcGF0aC5Kb2luKHJvb3QsInN0YXRlIiwic3RhdGUtdjIuanNvbiIpLExvY2tGaWxlOmZpbGVwYXRoLkpvaW4ocm9vdCwic3RhdGUiLCJtdXRhdGlvbi5sb2NrIiksT3BlcmF0aW9uc0RpcjpmaWxlcGF0aC5Kb2luKHJvb3QsInN0YXRlIiwib3BlcmF0aW9ucyIpLFBsdWdpbkRhdGFCYXNlOmZpbGVwYXRoLkpvaW4ocm9vdCwicGx1Z2luLWRhdGEiKSxNYW5hZ2VkUm9vdDpmaWxlcGF0aC5Kb2luKHJvb3QsIm1hbmFnZWQiKSxUZW1wUm9vdDpyLlRlbXBSb290LE9wZW5Db2RlUHJvYmVFbnZpcm9ubWVudDpbXXN0cmluZ3siUEFUSD0ifX0KCXN0YXRlLGVycjo9KHN0YXRldjIuU3RvcmV7UGF0aDpjZmcuU3RhdGVGaWxlfSkuTG9hZCgpOwoJaWYgZXJyIT1uaWwgewoJCXJldHVybiBuaWwsZXJyCgl9CglpZiBlcnI9cmVxdWlyZShzdGF0ZS5TY2hlbWFWZXJzaW9uPT1kb21haW4uU3RhdGVTY2hlbWFWZXJzaW9uLCJub25jdXJyZW50IHN0YXRlIHNjaGVtYSIpOwoJZXJyIT1uaWwgewoJCXJldHVybiBuaWwsZXJyCgl9Cgl2YXIgc2VsZWN0ZWQgKmRvbWFpbi5DbGllbnRCaW5kaW5nCglmb3IgXyxpOj1yYW5nZSBzdGF0ZS5JbnN0YWxsYXRpb25zIHsKCQlpZiBpLk5lZWRzUmViaW5kIHsKCQkJcmV0dXJuIG5pbCxlcnJvcnMuTmV3KCJwZW5kaW5nIHJlYmluZCIpCgkJfQoJCWZvciBfLGM6PXJhbmdlIGkuQ2xpZW50cyB7CgkJCWlmIGMuUGVuZGluZ05hdGl2ZUludGVudCE9bmlsIHx8IGMuTmF0aXZlQWN0aXZhdGlvbkF0dGVtcHQhPSIiIHx8IGMuTG9jYWxFbnRyeU9ic2VydmF0aW9uIT1uaWwgewoJCQkJcmV0dXJuIG5pbCxlcnJvcnMuTmV3KCJwZW5kaW5nL3Vua25vd24gbmF0aXZlIHN0YXRlIikKCQkJfQoJCQlpZiBpLkluc3RhbGxhdGlvbklEPT1iLkluc3RhbGxhdGlvbklEICYmIGMuQ2xpZW50QmluZGluZ0lEPT1iLkJpbmRpbmdJRCB7CgkJCQlpZiBzZWxlY3RlZCE9bmlsIHsKCQkJCQlyZXR1cm4gbmlsLGVycm9ycy5OZXcoImR1cGxpY2F0ZSBzZWxlY3RlZCBiaW5kaW5nIikKCQkJCX07CgkJCQl2Oj1jOwoJCQkJc2VsZWN0ZWQ9JnZ9CgkJfQoJfQoJZml4ZWQ6PXIuRml4ZWQKCWlmIHIuTW9kZSE9ImFic2VudCIgewoJCWlmIHNlbGVjdGVkPT1uaWwgfHwgc2VsZWN0ZWQuUHJvZmlsZUF1dGhvcml0eT09bmlsIHsKCQkJcmV0dXJuIG5pbCxlcnJvcnMuTmV3KCJzZWxlY3RlZCBvcmlnaW5hbCB0b2tlbiBtaXNzaW5nIikKCQl9CgkJYzo9KnNlbGVjdGVkCgkJaWYgZXJyPXJlcXVpcmUoYy5Qcm9maWxlTmFtZXNwYWNlPT1jZmcuU3RhdGVSb290ICYmIGMuUHJvZmlsZUF1dGhvcml0eS5GYWN0cygpLkNhbm9uaWNhbFJvb3Q9PWIuU2NvcGVSb290LCJmdWxsIHByb2ZpbGUgbmFtZXNwYWNlL3Jvb3QiKTsKCQllcnIhPW5pbCB7CgkJCXJldHVybiBuaWwsZXJyCgkJfQoJCWlmIHIuT3JpZ2luYWxBdXRob3JpdHkhPW5pbCAmJiAoIXIuT3JpZ2luYWxBdXRob3JpdHkuRXF1YWwoKmMuUHJvZmlsZUF1dGhvcml0eSkgfHwgci5PcmlnaW5hbE5hbWVzcGFjZSE9Yy5Qcm9maWxlTmFtZXNwYWNlKSB7CgkJCXJldHVybiBuaWwsZXJyb3JzLk5ldygib3JpZ2luYWwgZnVsbCB0b2tlbi9uYW1lc3BhY2UgY2hhbmdlZCIpCgkJfQoJCWYsb2s6PWMuU2VsZWN0ZWREZWxpdmVyeS5DdXJzb3JGYWN0cygpOwoJCWlmICFvayB7CgkJCXJldHVybiBuaWwsZXJyb3JzLk5ldygic2VsZWN0ZWQgQ3Vyc29yIHBhY2tldCBtaXNzaW5nIikKCQl9CgkJZXhlY3V0YWJsZSxlOj1wb3J0YWJsZS5SZXNvbHZlUHJpbWFyeUV4ZWN1dGFibGUoc25hcC5MZWRnZXIsYi5QcmltYXJ5KTsKCQlpZiBlIT1uaWwgewoJCQlyZXR1cm4gbmlsLGUKCQl9CgkJYWN0dWFsOj1jdXJzb3JpbnN0YWxsLkF1dGhvcml0eXtQcm9maWxlUm9vdDpmLlByb2ZpbGVSb290LEN1cnNvclZlcnNpb246Zi5DdXJzb3JWZXJzaW9uLFF1YWxpZmljYXRpb25JRDpmLlF1YWxpZmljYXRpb25JRCxFeGVjdXRhYmxlOmV4ZWN1dGFibGUsRXhlY3V0YWJsZURpZ2VzdDoic2hhMjU2OiIrc25hcC5MZWRnZXIuRmlsZXNbZXhlY3V0YWJsZV0uU0hBMjU2LFNlbGVjdG9yOmYuU2VsZWN0b3IsT2JqZWN0SUQ6Zi5PYmplY3RJRH0KCQlpZiBmaXhlZCE9bmlsICYmICpmaXhlZCE9YWN0dWFsIHsKCQkJcmV0dXJuIG5pbCxlcnJvcnMuTmV3KCJvcmlnaW5hbCBmaXhlZCBzZWxlY3Rpb24gY2hhbmdlZCIpCgkJfTsKCQlmaXhlZD0mYWN0dWFsCgkJaWYgZXJyPWIuQ2hlY2tTbmFwc2hvdChzbmFwKTsKCQllcnIhPW5pbCB7CgkJCXJldHVybiBuaWwsZXJyCgkJfQoJfSBlbHNlIHsKCQlpZiBzZWxlY3RlZCE9bmlsIHx8IHIuT3JpZ2luYWxBdXRob3JpdHk9PW5pbCB8fCByLk9yaWdpbmFsTmFtZXNwYWNlIT1jZmcuU3RhdGVSb290IHx8IGZpeGVkPT1uaWwgewoJCQlyZXR1cm4gbmlsLGVycm9ycy5OZXcoInNlbGVjdGVkIGJpbmRpbmcgcmVtYWlucy9hYnNlbnQgb3JpZ2luYWwgYXV0aG9yaXR5IikKCQl9CgkJaWYgZXJyPXByb2ZpbGVhdXRob3JpdHkuUmV2YWxpZGF0ZShjdHgsKnIuT3JpZ2luYWxBdXRob3JpdHkpOwoJCWVyciE9bmlsIHsKCQkJcmV0dXJuIG5pbCxlcnIKCQl9CgkJa2V5LF8sXyxlOj1iLlJlZ2lzdHJhdGlvbigpOwoJCWlmIGUhPW5pbCB7CgkJCXJldHVybiBuaWwsZQoJCX0KCQlpZiBfLG9rOj1zbmFwLkxlZGdlci5Db25zdW1lcnNba2V5XTsKCQlvayB7CgkJCXJldHVybiBuaWwsZXJyb3JzLk5ldygic2VsZWN0ZWQgY29uc3VtZXIgcmVtYWlucyIpCgkJfQoJCWlmIF8sZT1vcy5Mc3RhdChyLlNlbGVjdG9yKTsKCQkhZXJyb3JzLklzKGUsb3MuRXJyTm90RXhpc3QpIHsKCQkJcmV0dXJuIG5pbCxlcnJvcnMuTmV3KCJzZWxlY3RlZCBsb2NhdG9yIHJlbWFpbnMvdW5rbm93biIpCgkJfQoJfQoJYWRhcHRlcixlcnI6PWN1cnNvcmluc3RhbGwuTmV3KG5hdGl2ZWNvbmZpZy5OZXcoKSxwYXRocG9saWN5LlBvbGljeXt9LGZpeGVkKTsKCWlmIGVyciE9bmlsIHsKCQlyZXR1cm4gbmlsLGVycgoJfQoJcmVnaXN0cnksZXJyOj1jbGllbnRzLk5ld1JlZ2lzdHJ5KGFkYXB0ZXIpOwoJaWYgZXJyIT1uaWwgewoJCXJldHVybiBuaWwsZXJyCgl9OwoJY2ZnLlJlZ2lzdHJ5PXJlZ2lzdHJ5CgllbmdpbmUsZXJyOj11YXAuTmV3KGNmZyk7CglpZiBlcnIhPW5pbCB7CgkJcmV0dXJuIG5pbCxlcnIKCX0KCWluc3BlY3Rpb24sZXJyOj1lbmdpbmUuSW5zcGVjdChjdHgpOwoJaWYgZXJyIT1uaWwgewoJCXJldHVybiBuaWwsZXJyCgl9CglyZWNvdmVyeTo9aW5zcGVjdGlvbi5SZWNvdmVyeQoJaWYgcmVjb3ZlcnkuUmVxdWlyZWQgfHwgcmVjb3ZlcnkuUmVhc29uIT0iIiB8fCBsZW4ocmVjb3ZlcnkuSm91cm5hbHMpK2xlbihyZWNvdmVyeS5SZWNlaXB0cykrbGVuKHJlY292ZXJ5Lk5hdGl2ZUludGVudHMpIT0wIHsKCQlyZXR1cm4gbmlsLGVycm9ycy5OZXcoInBlbmRpbmcgcHVibGljIHJlY292ZXJ5IikKCX0KCW91dDo9bWFwW3N0cmluZ11hbnl7InN0YXRlIjpzdGF0ZSwiaW5zcGVjdGlvbiI6aW5zcGVjdGlvbiwiZml4ZWQiOmZpeGVkLCJiaW5kaW5nIjpiLCJtb2RlIjpyLk1vZGUsImJyaWRnZVNvdXJjZVNIQTI1NiI6cGFja2V0U291cmNlU0hBMjU2fQoJaWYgci5Nb2RlIT0iYWJzZW50IiB7CgkJaWYgZXJyPWVuZ2luZS5WZXJpZnlQcm9maWxlQXV0aG9yaXR5KGN0eCxiLkluc3RhbGxhdGlvbklELGIuQmluZGluZ0lEKTsKCQllcnIhPW5pbCB7CgkJCXJldHVybiBuaWwsZXJyCgkJfQoJCWlmIGVycj1wcm9maWxlYXV0aG9yaXR5LlJldmFsaWRhdGUoY3R4LCpzZWxlY3RlZC5Qcm9maWxlQXV0aG9yaXR5KTsKCQllcnIhPW5pbCB7CgkJCXJldHVybiBuaWwsZXJyCgkJfQoJCWlmIHIuT3JpZ2luYWxBdXRob3JpdHkhPW5pbCB7CgkJCWlmIGVycj1wcm9maWxlYXV0aG9yaXR5LlJldmFsaWRhdGUoY3R4LCpyLk9yaWdpbmFsQXV0aG9yaXR5KTsKCQkJZXJyIT1uaWwgewoJCQkJcmV0dXJuIG5pbCxlcnIKCQkJfX0KCQlnYXRlLGU6PWNvcGlsb3R2c2NvZGVpbnN0YWxsLk5ld0N1cnNvckdhdGUoYixjZmcsKmZpeGVkKTsKCQlpZiBlIT1uaWwgewoJCQlyZXR1cm4gbmlsLGUKCQl9CgkJY29uc3VtZXIsZTo9Z2F0ZS5Db25zdW1lckJpbmRpbmcoY3R4KTsKCQlpZiBlIT1uaWwgewoJCQlyZXR1cm4gbmlsLGUKCQl9CgkJZWZmZWN0aXZlLGU6PWdhdGUuRWZmZWN0aXZlQ29uZmlnKGN0eCk7CgkJaWYgZSE9bmlsIHsKCQkJcmV0dXJuIG5pbCxlCgkJfQoJCW91dFsic2VsZWN0ZWQiXT1zZWxlY3RlZDsKCQlvdXRbIm9yaWdpbmFsQXV0aG9yaXR5Il09c2VsZWN0ZWQuUHJvZmlsZUF1dGhvcml0eTsKCQlvdXRbIm9yaWdpbmFsTmFtZXNwYWNlIl09c2VsZWN0ZWQuUHJvZmlsZU5hbWVzcGFjZQoJCW91dFsiY29uc3VtZXJCaW5kaW5nIl09Y29uc3VtZXI7CgkJb3V0WyJjaGFubmVscyJdPWdhdGUuQ2hhbm5lbHMoY3R4LGNvbnN1bWVyKTsKCQlvdXRbImVmZmVjdGl2ZUNvbmZpZyJdPWVmZmVjdGl2ZQoJCWlmIGNvbnN1bWVyLkdlbmVyYXRpb24hPXNuYXAuTGVkZ2VyLkdlbmVyYXRpb24gewoJCQlyZXR1cm4gbmlsLGVycm9ycy5OZXcoImFjdHVhbCBjb21taXR0ZWQgZ2VuZXJhdGlvbiBjaGFuZ2VkIikKCQl9CgkJaWYgci5Nb2RlPT0icmVtb3ZlLXBsYW4iIHsKCQkJdmFyIHJlY2VpcHQgKmN1cnNvcmhvb2tzLlJlY2VpcHQKCQkJZm9yIF8sb2JqZWN0Oj1yYW5nZSBzZWxlY3RlZC5OYXRpdmVPYmplY3RzIHsKCQkJCWlmIG9iamVjdC5LaW5kPT0iY3Vyc29yX3VzZXJfc3RvcCIgewoJCQkJCWlmIHJlY2VpcHQhPW5pbCB7CgkJCQkJCXJldHVybiBuaWwsZXJyb3JzLk5ldygiZHVwbGljYXRlIHN0b3Agb3duZXJzaGlwIikKCQkJCQl9OwoJCQkJCXY6PW9iamVjdC5DdXJzb3JSZWNlaXB0OwoJCQkJCXJlY2VpcHQ9JmN1cnNvcmhvb2tzLlJlY2VpcHR7VmVyc2lvbjp2LlZlcnNpb24sRXZlbnQ6di5FdmVudCxTaGVsbDpjdXJzb3Job29rcy5TaGVsbENvbnRyYWN0KHYuU2hlbGwpLFNwZWM6Y3Vyc29yaG9va3MuSG9va1NwZWN7RXhlY3V0YWJsZTp2LkV4ZWN1dGFibGUsU2VsZWN0b3I6di5TZWxlY3Rvcn0sRW50cnlEaWdlc3Q6di5FbnRyeURpZ2VzdCxSZW1haW5kZXJEaWdlc3Q6di5SZW1haW5kZXJEaWdlc3R9fX0KCQkJaWYgcmVjZWlwdD09bmlsIHsKCQkJCXJldHVybiBuaWwsZXJyb3JzLk5ldygiYWNrbm93bGVkZ2VkIHN0b3Agb3duZXJzaGlwIG1pc3NpbmciKQoJCQl9CgkJCWZpbGUsZTo9bmF0aXZlY29uZmlnLk5ldygpLlJlYWRFeGFjdEZpbGUoZmlsZXBhdGguSm9pbihiLlNjb3BlUm9vdCwiaG9va3MuanNvbiIpKTsKCQkJaWYgZSE9bmlsIHsKCQkJCXJldHVybiBuaWwsZQoJCQl9CgkJCS8vIEV4cG9ydGVkIHB1cmUgUGxhbihSZW1vdmUpIGlzIHRoaXMgc291cmNlJ3MgUGxhblJlbW92ZTsgbmV2ZXIgQXBwbHkvQUNLLgoJCQlwbGFuLGU6PWN1cnNvcmhvb2tzLlBsYW4oY3Vyc29yaG9va3MuUmVxdWVzdHtEb2N1bWVudDpmaWxlLkJvZHksT3BlcmF0aW9uOmN1cnNvcmhvb2tzLlJlbW92ZSxQcmV2aW91czpyZWNlaXB0LFNoZWxsOnJlY2VpcHQuU2hlbGx9KTsKCQkJaWYgZSE9bmlsIHsKCQkJCXJldHVybiBuaWwsZQoJCQl9CgkJCWlmIHBsYW4uQ29uZmxpY3QgfHwgcGxhbi5SZWNlaXB0IT1uaWwgewoJCQkJcmV0dXJuIG5pbCxlcnJvcnMuTmV3KCJ1bnByb3ZlZCBwdXJlIHJlbW92YWwiKQoJCQl9CgkJCW91dFsiZXhwZWN0ZWRIb29rQnl0ZXMiXT1wbGFuLkRlc2lyZWQ7CgkJCW91dFsiZXhwZWN0ZWRIb29rU0hBMjU2Il09Zm10LlNwcmludGYoIiV4IixzaGEyNTYuU3VtMjU2KHBsYW4uRGVzaXJlZCkpCgkJfQoJfQoJaWYgZXJyPWN0eC5FcnIoKTsKCWVyciE9bmlsIHsKCQlyZXR1cm4gbmlsLGVycgoJfTsKCXJldHVybiBvdXQsbmlsCn0KZnVuYyBtYWluKCkgewoJcmF3LGVycjo9aW8uUmVhZEFsbChpby5MaW1pdFJlYWRlcihvcy5TdGRpbiwoMjw8MjApKzEpKTsKCWlmIGVyciE9bmlsIHx8IGxlbihyYXcpPjI8PDIwIHsKCQlmbXQuRnByaW50bG4ob3MuU3RkZXJyLCJib3VuZGVkIHJlcXVlc3QgcmVxdWlyZWQiKTsKCQlvcy5FeGl0KDIpCgl9CglkZWNvZGVyOj1qc29uLk5ld0RlY29kZXIoYnl0ZXMuTmV3UmVhZGVyKHJhdykpOwoJZGVjb2Rlci5EaXNhbGxvd1Vua25vd25GaWVsZHMoKTsKCXZhciByIHJlcXVlc3QKCWlmIGVycj1kZWNvZGVyLkRlY29kZSgmcik7CgllcnI9PW5pbCB7dmFyIGV4dHJhIGFueTsKCQlpZiBlOj1kZWNvZGVyLkRlY29kZSgmZXh0cmEpOwoJCSFlcnJvcnMuSXMoZSxpby5FT0YpIHsKCQkJZXJyPWVycm9ycy5OZXcoInRyYWlsaW5nIHJlcXVlc3QiKQoJCX19CglpZiBlcnI9PW5pbCAmJiByLk1vZGUhPSJsaXZlIiAmJiByLk1vZGUhPSJyZW1vdmUtcGxhbiIgJiYgci5Nb2RlIT0iYWJzZW50IiB7CgkJZXJyPWVycm9ycy5OZXcoInVua25vd24gcmVhZGJhY2sgbW9kZSIpCgl9Cgl2YXIgcmVzdWx0IG1hcFtzdHJpbmddYW55OwoJaWYgZXJyPT1uaWwgewoJCXJlc3VsdCxlcnI9cmVhZGJhY2socikKCX0KCWlmIGVyciE9bmlsIHsKCQlmbXQuRnByaW50bG4ob3MuU3RkZXJyLGVycik7CgkJb3MuRXhpdCgyKQoJfQoJZW5jb2RlZCxlcnI6PWpzb24uTWFyc2hhbChyZXN1bHQpOwoJaWYgZXJyIT1uaWwgfHwgbGVuKGVuY29kZWQpPjE2PDwyMCB7CgkJZm10LkZwcmludGxuKG9zLlN0ZGVyciwiYm91bmRlZCByZWFkYmFjayByZXF1aXJlZCIpOwoJCW9zLkV4aXQoMikKCX0KCWZtdC5QcmludGxuKHN0cmluZyhlbmNvZGVkKSkKfQo=')
WIRE_BYTES = b'"""Source-pinned wire recipe only. Does not import vendor code or launch anything.\nCoordinator may import these local helpers into a finite loopback HTTP fixture.\nThis module has no main block, network access, credentials, or native execution.\n"""\nimport struct\n\ndef varint(value):\n    assert value >= 0\n    result = bytearray()\n    while value > 127:\n        result.append((value & 127) | 128)\n        value >>= 7\n    result.append(value)\n    return bytes(result)\n\ndef uint(field, value):\n    return varint(field << 3) + varint(value)\n\ndef blob(field, value):\n    if isinstance(value, str):\n        value = value.encode(\'utf-8\')\n    return varint((field << 3) | 2) + varint(len(value)) + value\n\ndef frame(payload, flags=0):\n    return bytes([flags]) + struct.pack(\'>I\', len(payload)) + payload\n\ndef read_varint(data, at):\n    value = 0\n    for shift in range(0, 70, 7):\n        if at >= len(data):\n            raise ValueError(\'truncated varint\')\n        byte = data[at]\n        at += 1\n        value |= (byte & 127) << shift\n        if byte < 128:\n            return value, at\n    raise ValueError(\'oversized varint\')\n\ndef fields(data):\n    """Bounded wire scanner; repeated fields remain repeated. No vendor schema eval."""\n    if len(data) > 1024 * 1024:\n        raise ValueError(\'body budget\')\n    at = 0\n    result = []\n    while at < len(data):\n        tag, at = read_varint(data, at)\n        number, wire = tag >> 3, tag & 7\n        if number == 0:\n            raise ValueError(\'zero tag\')\n        if wire == 0:\n            value, at = read_varint(data, at)\n        elif wire == 2:\n            size, at = read_varint(data, at)\n            value = data[at:at + size]\n            at += size\n        elif wire in (1, 5):\n            size = 8 if wire == 1 else 4\n            value = data[at:at + size]\n            at += size\n        else:\n            raise ValueError(\'unsupported wire type\')\n        if at > len(data):\n            raise ValueError(\'truncated field\')\n        result.append((number, wire, value))\n    return result\n\ndef first(data, number, default=None):\n    return next((value for n, wire, value in fields(data) if n == number), default)\n\ndef model_details(model_id=\'TEST-native-stop\'):\n    return b\'\'.join(blob(n, model_id) for n in (1, 3, 4, 5))\n\ndef unary_responses():\n    """Only the native HTTP/1.1 Connect/protobuf callsites traced in this audit.\n    GetUsableModels/DefaultModel here are AiService paths, despite AgentService\n    descriptors also exposing identically named methods.\n    """\n    return {\n        \'/aiserver.v1.ServerConfigService/GetServerConfig\': uint(7, 1),\n        \'/aiserver.v1.AiService/GetUsableModels\': blob(1, model_details()),\n        \'/aiserver.v1.AiService/GetDefaultModelForCli\': blob(1, model_details()),\n        \'/aiserver.v1.AiService/AvailableModels\': b\'\',\n        \'/aiserver.v1.DashboardService/GetMe\': blob(1, \'TEST-loopback-identity\') + uint(2, 1),\n        \'/aiserver.v1.DashboardService/GetUserPrivacyMode\': b\'\',\n        \'/aiserver.v1.DashboardService/GetTeamAdminSettingsOrEmptyIfNotInTeam\': b\'\',\n        \'/aiserver.v1.DashboardService/GetEffectiveUserPlugins\': b\'\',\n        \'/aiserver.v1.DashboardService/GetManagedSkills\': b\'\',\n    }\n\ndef decode_append(body):\n    """Input is unframed unary BidiAppendRequest protobuf (decompress first)."""\n    request_id = first(first(body, 2), 1).decode()\n    sequence = first(body, 3, 0)\n    data = first(body, 4) or bytes.fromhex(first(body, 1, b\'\').decode(\'ascii\'))\n    return request_id, sequence, data\n\ndef stop_frame(conversation_id, generation_id, model_id=\'TEST-native-stop\'):\n    query = (blob(1, \'completed\') + uint(2, 0) + blob(3, conversation_id)\n             + (blob(4, generation_id) if generation_id else b\'\')\n             + blob(5, model_id) + blob(6, model_id))\n    # AgentServerMessage.execServerMessage(2) -> ExecServerMessage:\n    # id(1), execId(15), executeHookArgs(27) -> request(1) -> stop(11).\n    args = blob(1, blob(11, query))\n    message = uint(1, 1) + blob(15, \'TEST-stop-1\') + blob(27, args)\n    return frame(blob(2, message))\n\ndef text_frame(text=\'TEST deterministic turn complete\'):\n    # AgentServerMessage.interactionUpdate(1) -> textDelta(1) -> text(1).\n    return frame(blob(1, blob(1, blob(1, text))))\n\ndef terminal_frames():\n    # interactionUpdate(1) -> turnEnded(14), followed by Connect EndStream JSON.\n    return frame(blob(1, blob(14, b\'\'))) + frame(b\'{}\', 2)\n\ndef classify_client_message(data):\n    run = first(data, 1)\n    if run is not None:\n        return {\'kind\': \'run\', \'conversation\': first(run, 5, b\'\').decode(),\n                \'generation\': first(run, 25, b\'\').decode(),\n                \'model\': first(first(run, 3, b\'\'), 1, b\'\').decode()}\n    exec_result = first(data, 2)\n    if exec_result is not None:\n        hook = first(exec_result, 27)\n        if hook is None:\n            return {\'kind\': \'unexpected-exec-result\'}\n        stop_response = first(first(hook, 1, b\'\'), 11)\n        return {\'kind\': \'stop-result\', \'id\': first(exec_result, 1, 0),\n                \'execId\': first(exec_result, 15, b\'\').decode(),\n                \'hasStop\': stop_response is not None,\n                \'followup\': first(stop_response or b\'\', 1, b\'\').decode()}\n    control = first(data, 5)\n    if control is not None:\n        if first(control, 2) is not None:\n            return {\'kind\': \'exec-throw\'}\n        close = first(control, 1)\n        return {\'kind\': \'exec-close\', \'id\': first(close, 1, 0)} if close is not None else {\'kind\': \'exec-control\'}\n    if first(data, 7) is not None:\n        return {\'kind\': \'heartbeat\'}\n    return {\'kind\': \'unexpected-client-message\'}\n'
ARTIFACT_PINS = [{'file': 'index.js', 'sha256': '45d9b1df85d0165cb2e690f96fa5fbe4b59e8a71ddd22176a301ba7e5b18a0b9', 'byteLength': 7549229}, {'file': '1538.index.js', 'sha256': '62f61c0b6ccba0f43e79eaf947e33aa33640969050f6459aeec17360aefa1b60', 'byteLength': 5940}, {'file': '4114.index.js', 'sha256': '43386ebdc3a4af020c5398fd020083d8103ef7fbf286970d2d71d3ef9dade9df', 'byteLength': 5699}, {'file': '9725.index.js', 'sha256': '2d2c791377dfe432b3ff1f3173e5218efad7deee76e35a1fc632a577f7fe4440', 'byteLength': 6062}, {'file': 'diff-patch-worker.js', 'sha256': '8de87d1ba7886c6135522679c5d29756afcefb0fe91bdec2023cd42c911a4d0d', 'byteLength': 6646}, {'file': '7450.index.js', 'sha256': '703707729125fa35af87c15c640285d9b90b4a4cd099cfecff0e28c79c8f5f56', 'byteLength': 1341}, {'file': '478.index.js', 'sha256': '663ed1eb66a692521cc7c8bd89ed352ada8ce32da3c8138ca2813958afb9bd00', 'byteLength': 14521}, {'file': '8914.index.js', 'sha256': '0e48ce732c3866c95e3956c52cad892a74a8e7aca9d65090edc6227aad604902', 'byteLength': 671}, {'file': '3541.index.js', 'sha256': '57b1e8de43deb2e3cb8da14522189a21964ff83f915582b20c4073eb03969dc4', 'byteLength': 4469}, {'file': 'unified-diff-worker.js', 'sha256': '0191e7623aab46a080eb5752018aeb209723645558a33203e31d98d419694188', 'byteLength': 6786}, {'file': 'pdf-worker.js', 'sha256': '34cc2e8b9c321799f7d55997c42c8ccc6abc9ea902fd7c7845da5accb136a948', 'byteLength': 480099}, {'file': '5092.index.js', 'sha256': 'cdb601e4e52224c8e36885130c33241a903b87b1f0e1b897b47f1ff79ae66d4e', 'byteLength': 16085}, {'file': '6136.index.js', 'sha256': 'e1000a10d7d6b2b0ad897abcadd43177267d1c18baa907861dd19eb9708eddca', 'byteLength': 97261}, {'file': '8391.index.js', 'sha256': 'bb1f809662186fa081a1e365ce57202797bf44db1f31c071af9edbe56c9ef49a', 'byteLength': 3435}, {'file': '3575.index.js', 'sha256': '3470b1d226b47d6148f4586cabe05aebd77feeafb14d5c854c90483066406da5', 'byteLength': 13815}, {'file': '4760.index.js', 'sha256': 'a08b490045a975d5e86dd849e8b7c3c7c8c2773e27a2e9e4580b68dc425e0485', 'byteLength': 2099}, {'file': '8096.index.js.LICENSE.txt', 'sha256': '806fb10cfb2aed86c6b7b5a179ea01c9c3d736183a3de70ef1e355259bf4b52e', 'byteLength': 2659}, {'file': '3244.index.js', 'sha256': '4566b4210aef42ee023901a74a0318b5e61eb755ffbe6cf52497b67f73e17665', 'byteLength': 899}, {'file': 'cursor-askpass.js', 'sha256': 'ffb6b47c511bdbe2cab68e52ef6e3d37b5bf0ac1e632c78725e30ddb2dbde590', 'byteLength': 5653}, {'file': '8891.index.js', 'sha256': 'b9d93a11a3e4bdad0ddab4e7e5a88d2aba678a0ef42f2bbb5c4a37ff1fa871bc', 'byteLength': 3241578}, {'file': '8644.index.js', 'sha256': 'ec890f0f3073142cba2a6b14541e17f5966b68733a198b6a40be7f60f437fa02', 'byteLength': 7271}, {'file': '3284.index.js', 'sha256': '373a713f8b9f6dad4c2542885905bbb58a7b7c858250628f3088b29649448eaf', 'byteLength': 573}, {'file': '2576.index.js', 'sha256': 'e73fc1ae6529a431ccd5278b39e4cb6bb86fbe0eed6a76684950a2b3611e3717', 'byteLength': 2224}, {'file': '5336.index.js', 'sha256': '7d9543dd6ee0e75c69a18950022cd93031343c29bb8d36f678b5d8b6606a7ce0', 'byteLength': 25190}, {'file': '7923.index.js', 'sha256': 'b5333ad5328c6aa394fd6631822b5caf63a67019c1b9521e861a522ddefbbc4a', 'byteLength': 15734}, {'file': '5818.index.js', 'sha256': 'c94b3f56ed6a8555169088cb186cc2ee0917d7f4412b1921a177817f0df1258e', 'byteLength': 9840}, {'file': '190.index.js', 'sha256': '73c1e9693be17a8b6087d62a11a2159d962371d13c69869f1816a95a1cae547d', 'byteLength': 115505}, {'file': '6918.index.js', 'sha256': 'ba224f3f4d27ca1b5243a75d667b4698543993b379c9551bf9e012317845cad5', 'byteLength': 3587}, {'file': '8096.index.js', 'sha256': '99b2da77d52174213307274bf428c95ca3f7c1216c27630e49c928cfef1b2442', 'byteLength': 2723244}, {'file': '3550.index.js', 'sha256': 'e7636cd64f760f486773640de92cdd11350597e323f1981ce4ac8f401fc00111', 'byteLength': 4849}, {'file': '1429.index.js', 'sha256': '8543b0e5c57f0e33350a64707b341d949432409136ff45b0c97cdf66f6edad29', 'byteLength': 43650}, {'file': '5531.index.js', 'sha256': '3b97993589db91c120031cbb838fb8b32f4b53f611dbb9e954e01b212e2888f3', 'byteLength': 899}, {'file': '2290.index.js', 'sha256': '9f23f7dcfb33f41666f03a4235acfa63a9dbade1889946eca92c31aea3f6096a', 'byteLength': 35299}, {'file': '159.index.js', 'sha256': '66469a19bbe5023910ea7024ecbb54d3985a4a536657b4f4ca2b160b641ef1ac', 'byteLength': 341387}, {'file': '2911.index.js', 'sha256': '863ea507c6237d077c41a5feb5561acca0b95a6d13e3d80e0644ebe9d0ff773c', 'byteLength': 6282}, {'file': '4343.index.js', 'sha256': 'b02e2b0971ed8ea980a1d311fa4b2d314c365daee468944c85328cc126e1298a', 'byteLength': 665}, {'file': '1326.index.js', 'sha256': '24c9f33e87969bf629e8f9ffef267373d120bf55ef8d91375d29cd693399eae7', 'byteLength': 36858}, {'file': '5993.index.js', 'sha256': '25502e95a9b84c20dbf1e4d12daefad5cd162ae9f0fec6c59be8533524dc538f', 'byteLength': 1706}, {'file': '1623.index.js', 'sha256': '6e393636a7898efe8b9a52463682d7cddf211e668334aed58f6864b37eaef60d', 'byteLength': 116131}, {'file': '2721.index.js', 'sha256': '16ba2d3946cb1df6bfad6dbc4d7c974ca697627081ff1834da73a1b4f04c8a15', 'byteLength': 89633}, {'file': '7000.index.js', 'sha256': '749a10b62a2094860d219683b5c5bb36e12c1fb036fcd31e08789f0085b2f5bd', 'byteLength': 981259}, {'file': '6643.index.js', 'sha256': 'c7b9d6d3c3713dc213e81e26b8c5649d28cfbc368ededcc3ec29684e513b9b6e', 'byteLength': 1499}, {'file': '4155.index.js', 'sha256': '1180c477779e5c338b4264b0d671c8c458d4e444188d1e3bd74b5b482f6b5c75', 'byteLength': 9134}, {'file': '9322.index.js', 'sha256': 'ef4157396957372c84013c508e89bed33c20b020185b63ab307cd3929fca9766', 'byteLength': 8866}, {'file': '6536.index.js', 'sha256': 'd777b3cfcc2aa3f42eb0b22cb91480c31482a1c669c6942dc475c89491db9fd3', 'byteLength': 35558}, {'file': 'package.json', 'sha256': '625a6b5d64e45fd989706ab6fd21a95f454d4e70cab7c0c1c151d37582d8bee2', 'byteLength': 64}, {'file': '2761.index.js', 'sha256': 'd3bb797ae5229578f33adbe6413e151ba4ee50c022c054eb6f68a93f47f683d8', 'byteLength': 5825}, {'file': 'pdf-worker.js.LICENSE.txt', 'sha256': 'b5e8c493d516d9b156045ec3b83cc3e26785363af56db22bd52382942b394242', 'byteLength': 795}, {'file': '2422.index.js', 'sha256': 'cc119c5eccfbcf35112697c442c7bfba6670065f8c664a05d5040cce8dfc3528', 'byteLength': 1966}, {'file': '4945.index.js', 'sha256': 'e6b1a2a2d0a7974dd9ad5920483cecbcc1c3c76a9dc1978d11a081cab62e2df6', 'byteLength': 5987}, {'file': '3144.index.js', 'sha256': 'a66c828d56fdb0aee05e2d3023a8a66fe0e058a48b201f03b31d056b8e0cc591', 'byteLength': 793}, {'file': '9106.index.js', 'sha256': '4ce5d0eedcb45448c7a99350864f50122e739aaa9d9ecd12e25babd75dcda55a', 'byteLength': 387131}, {'file': '6174.index.js', 'sha256': '6bddd90df806756c130b3ebf4e74b0062cf0c030e6caa6e6537bf844e86d9496', 'byteLength': 15810}, {'file': '3857.index.js', 'sha256': '2bc55395422ae75b29d2ed5e21881cd12a060c16b5c3e913ea528eb28e117283', 'byteLength': 6881}, {'file': 'mac-webp-runtime.cjs', 'sha256': '4a6041caf835ff53c58caf36e679f65fe6c04722661f315de036ceddd107a770', 'byteLength': 309}, {'file': '3464.index.js', 'sha256': '030e649c6f5550ce08bfd657a9f07802ceecae568603e558e7fdac723cf86188', 'byteLength': 2438}, {'file': '8192.index.js', 'sha256': '06cb735afe2dd4a44d4be16d7e27bf724efbab6158aacb50c6880c4ed4cc50f2', 'byteLength': 70472}, {'file': '4632.index.js', 'sha256': '4600e9ea74d09c839889bc225530f55b9d104c8f4b2608081cd505b266f7c9e8', 'byteLength': 21998}, {'file': '9063.index.js', 'sha256': 'c425976b1aaf6963966103a3d6a7f24fdb9a38a3028daeac0e88f1509356bd95', 'byteLength': 9811}, {'file': '2734.index.js', 'sha256': 'a7c877a8dbaac4d1db3171939f9f7d6f397e9efcdbc2662d35fe0ffc054a56ec', 'byteLength': 26555}, {'file': '272.index.js', 'sha256': 'f996e8b2ec65ca48aaaa5adf9b1cd167c04c32b9a278342ceadafc8bf3a23180', 'byteLength': 6881}, {'file': 'a22718674812ee697cf3.js', 'sha256': 'ba885e166fb261506969fab7703b665d96998e6f995c8c4e8172c696f58574d6', 'byteLength': 2821}, {'file': '7351.index.js', 'sha256': '86745bbc5f2c74890e3cd5358ab4b02b0995db7558c9717abebe66a0beace206', 'byteLength': 1972}, {'file': 'diff-worker.js', 'sha256': 'f8145d47eb97535413a4ae9426c2562801ec0cffa3a4734d3bf444f7a74ca994', 'byteLength': 5945}, {'file': 'index.js.LICENSE.txt', 'sha256': 'ef63fcca608d4374bbfc9d807e32d54d272976a9ea53651471b1ed4f711bcf2b', 'byteLength': 1719}, {'file': '6436.index.js', 'sha256': '592623afe824b6db6b315e0d6a62b0279b75d8d3a4e4b297c3b28a81f08f2f54', 'byteLength': 11976}, {'file': '5740.index.js', 'sha256': 'e902aaa02a8596697eb069f2089767e61a07c168bea26db1143ea160ff7c77f2', 'byteLength': 5657}, {'file': '8991.index.js', 'sha256': 'd9645dfed24e004fe9f0d4e81df707832f2e9f0d3e4c74d7fabfc5f58cc25f3c', 'byteLength': 5435}, {'file': '2601.index.js', 'sha256': 'cf21effd409e730cc29155e5e81b76fef1ce836bfa00a1b1ef498e9065898000', 'byteLength': 14540}, {'file': '9147.index.js', 'sha256': '5fbf0cb6086f7556bf969d015bd6d3c5c209d63edf3256811145bad65716d858', 'byteLength': 340}, {'file': '5380.index.js', 'sha256': '25af69ecff6f7b1e8c12b7138122dbf040eb01144bd1b58a699c1ad75b0ce16e', 'byteLength': 58595}, {'file': '5434.index.js', 'sha256': '94b9bd7a100ac91499c1aaf7af4ec705ab1171ccec5144ace7bd824cb4ba902e', 'byteLength': 1085}, {'file': '7945.index.js', 'sha256': '399ffc412f6d14332bf44b51aa28cff40db56c25974d50fa58adefd468bb3a3b', 'byteLength': 364902}, {'file': '5720.index.js', 'sha256': '48b1bf10573d26f1cef098e5b19e2bd71177e336c1a70fc4edb9aa68991c8e0c', 'byteLength': 2419}, {'file': '5469.index.js', 'sha256': 'bccf3d9433cce7eb2a314835ffd68260158c86d1b5a85ccda7b28661f8c13039', 'byteLength': 336}, {'file': '3190.index.js', 'sha256': 'c6b0007358a0d92c1df1aa7e20c2863f94ba4732ca88334a686799b4fa4ca37e', 'byteLength': 1795}, {'file': '7373.index.js', 'sha256': '95f1e630a2fe054882701d86d447d9da645e9cdfbe25fe45123843f78f7db273', 'byteLength': 12363}, {'file': '6134.index.js', 'sha256': '356de23c9365c444bffe8e3f65982e49f6f3b181ffbfb38938e4aa3d77d1b261', 'byteLength': 7910}, {'file': '1499.index.js', 'sha256': 'c3905ad7c580b8fcc3e559ca316e3e125b7adc7a832c9dff5c7a9f10c41b5728', 'byteLength': 9146}, {'file': '2240.index.js', 'sha256': 'a6a95525446982366afb71c09241a6e7e8f31c0d409458e9928b4cf469d4830c', 'byteLength': 221412}, {'file': '6870.index.js', 'sha256': '8cd41ad250670e7162ce25daeb4b52a04356bd4a609ebf23eda719968e3ce18c', 'byteLength': 234}, {'file': '4161.index.js', 'sha256': '02a3a1a75d7951917815c42bc6e0b08d7f3af47fc0e9c1789fcb9b1c127e2810', 'byteLength': 5594}, {'file': '9494.index.js', 'sha256': '93ae84b5f457e1447331367d90ebc36f89e4d1754fba040e729b2e5ad73e1260', 'byteLength': 47082}, {'file': '7627.index.js', 'sha256': 'aa61aa5788a135dcf95dded5a94ce03f29586d53438e25877340d38260290f35', 'byteLength': 4893}, {'file': '903.index.js', 'sha256': '98537c9b472fb235ee58268a7612b9d86885851074af1274962ec012089d6c8a', 'byteLength': 9590}, {'file': 'node_modules/node-gyp-build/index.js', 'sha256': 'a7ed0d5ae218a19bdbdf15a590d0893790ddf536313b66a787554693cfaae078', 'byteLength': 390}, {'file': 'node_modules/node-gyp-build/build-test.js', 'sha256': '34c19ff8b6675d6d27c63a7df44d77a442805eeea8756d1c89e0264f4a3028f6', 'byteLength': 398}, {'file': 'node_modules/node-gyp-build/optional.js', 'sha256': 'e0b3a3a04166e6ecf1020cb31c0c4a54432c16d6d88714bd4de2214cf67dec81', 'byteLength': 143}, {'file': 'node_modules/node-gyp-build/package.json', 'sha256': '9e8def3fbf123e28aa1ca4b6aa557fba4e66eecf6e86d170b61ec1c7ed51305d', 'byteLength': 1004}, {'file': 'node_modules/node-gyp-build/bin.js', 'sha256': 'dc022adc093b9b79e1a0ef4b6bc3e6a52e3d627102113dad4afacbbe81f13a26', 'byteLength': 2094}, {'file': 'node_modules/node-gyp-build/node-gyp-build.js', 'sha256': '134f0585f7c665db89f332a379158c6f113274422e42aaf54e0aa9d5ac37f577', 'byteLength': 6078}, {'file': 'node_modules/file-uri-to-path/index.js', 'sha256': 'e62293e871bdd5a7449ff3c7956c9536ec1d2ea7369461de77322b5256bb93e7', 'byteLength': 1723}, {'file': 'node_modules/file-uri-to-path/package.json', 'sha256': '71eb1e24bb9694f89c613fa0aa307f977dd43f41d11794c7b48fabf6c55f66b0', 'byteLength': 717}, {'file': 'node_modules/better-sqlite3/package.json', 'sha256': '4665126292eff5dffe6c0ffabf98c4de18e479aa0b53edd775028eb567b3ecc1', 'byteLength': 1436}, {'file': 'node_modules/tree-sitter-bash/grammar.js', 'sha256': 'e1e7ed6840a2a692a1bd5f352ff85b38d065cdca200f95b1288230fb621ff2d7', 'byteLength': 30081}, {'file': 'node_modules/tree-sitter-bash/package.json', 'sha256': 'ee9d6515031266905123792f891fc9c3db75f55c5d9c1e027a315a60a09557c4', 'byteLength': 1616}, {'file': 'node_modules/piscina/package.json', 'sha256': '2a51b866d734d494f814027cbf422db40f8a2f14e8ac1e1b8f5b471f9bb83d52', 'byteLength': 2842}, {'file': 'node_modules/tree-sitter/index.js', 'sha256': 'ba5e8461e5068f2b58ea860aea331ed0c6d8a25f21142d75a6145a1544fc9ffe', 'byteLength': 25716}, {'file': 'node_modules/tree-sitter/package.json', 'sha256': '8cca5044dd3381dbf958dea36ef6d9f81f379886d32ef845919cf07679c6e42c', 'byteLength': 1757}, {'file': 'node_modules/bindings/bindings.js', 'sha256': '8e32a0d37f20bd6f7d5bdbf99d041aa27be47cbbe5172ac13ebf7380a10b3bf6', 'byteLength': 5986}, {'file': 'node_modules/bindings/package.json', 'sha256': 'a87721fe406e1f1798fef44d697b46ea1efe346fda118010334713346ee4207c', 'byteLength': 660}, {'file': 'node_modules/file-uri-to-path/test/tests.json', 'sha256': 'b48c50c1328ab8522fc0bf91923ea42fb59e63e847b967dac88123cb5e5b185d', 'byteLength': 810}, {'file': 'node_modules/file-uri-to-path/test/test.js', 'sha256': '223c25dba6fefbb7cb491b026a32f070b1425475d3cd2d04627be5d71a07fd38', 'byteLength': 666}, {'file': 'node_modules/better-sqlite3/deps/copy.js', 'sha256': '05a2bd41dbd96e33e2fc6cf4bcbc722b4bc8c529813b882f92113d7dbbbece67', 'byteLength': 897}, {'file': 'node_modules/better-sqlite3/lib/index.js', 'sha256': '82db11c4ee43a41d859988c5db42c3771dff565371f94bacbd1e4d8d6ceb47cd', 'byteLength': 110}, {'file': 'node_modules/better-sqlite3/lib/sqlite-error.js', 'sha256': '2582d61c27680dead168543f392eb102be621dfbef282a4ca4c7c21aa5e7c75d', 'byteLength': 717}, {'file': 'node_modules/better-sqlite3/lib/database.js', 'sha256': '02ea23bdd23d7ac5de0675a2f32fc686e76d5c6a32bd3e3891f360e72e07f61f', 'byteLength': 4149}, {'file': 'node_modules/better-sqlite3/lib/util.js', 'sha256': '92b2e39e2151b43a2252e10b6d6de876ecaf0008336a4fa1dfe1317b20f1916f', 'byteLength': 331}, {'file': 'node_modules/better-sqlite3/lib/methods/serialize.js', 'sha256': '7a10ee5c2735384b7f0c361811bc6d017db29f62b203fd3c68a35f667e2c2605', 'byteLength': 625}, {'file': 'node_modules/better-sqlite3/lib/methods/function.js', 'sha256': 'f431d49303b8bbdc044b1f1b455bdad21fc9b74b007de0acb22f08f25b4febd3', 'byteLength': 1396}, {'file': 'node_modules/better-sqlite3/lib/methods/backup.js', 'sha256': 'ea29d34992bb02e006d0fdeda9675ac5d2bb227aaf57468decd997e9fc9c7dbf', 'byteLength': 2380}, {'file': 'node_modules/better-sqlite3/lib/methods/table.js', 'sha256': '97c42d9ded1aa96c7d916b5b92f96b4e59581d50eaf629cd2c7afb78ff26a9ea', 'byteLength': 7144}, {'file': 'node_modules/better-sqlite3/lib/methods/wrappers.js', 'sha256': 'a150a6271d23f4e5f8953b129f370ff096c7cdc4b812afbf080a6cf4ab741bcf', 'byteLength': 1145}, {'file': 'node_modules/better-sqlite3/lib/methods/aggregate.js', 'sha256': 'e9f74eb919ec93fe089c95ddf25a98f1f631c80418fa34fb2346ca1bc29f1b82', 'byteLength': 1932}, {'file': 'node_modules/better-sqlite3/lib/methods/pragma.js', 'sha256': '8b1c54475bd4340b15e25c50d53d06308be65f8f919ecbe4aa9d285ca859ad5a', 'byteLength': 536}, {'file': 'node_modules/better-sqlite3/lib/methods/transaction.js', 'sha256': 'bc8624a3ef689d8f78e5669020ad121e17acbc93b1d5ee5afe26860b1084c66c', 'byteLength': 2792}, {'file': 'node_modules/better-sqlite3/lib/methods/inspect.js', 'sha256': '4975a78daee850adee62ba98719d0f223819a0ec135a07c0e302994bd8dbff61', 'byteLength': 174}, {'file': 'node_modules/tree-sitter-bash/src/node-types.json', 'sha256': '39763582e682b132eb5349af6e1d7be78aed98decf808ffa6bcf92cfed4ac5dc', 'byteLength': 49267}, {'file': 'node_modules/tree-sitter-bash/src/grammar.json', 'sha256': '899dc858b05efefb478dcefc5491b5653e2df5d2aca16292d6def5c0746ac820', 'byteLength': 179302}, {'file': 'node_modules/tree-sitter-bash/bindings/node/index.js', 'sha256': '609bdf22b8fd183025b3c24e8587f6211d50358e8f2e4c526b9217349ed9dbc0', 'byteLength': 201}, {'file': 'node_modules/piscina/benchmark/simple-benchmark-fixed-queue.js', 'sha256': '66ab8b3ee623b26d77460a3aa954890605e3407944654891bf1919d22582ce1a', 'byteLength': 801}, {'file': 'node_modules/piscina/benchmark/simple-benchmark.js', 'sha256': '10ec3fdba4616419d0d6ca8984c3d16db2d65543f19fe539eff08655e3fcc50c', 'byteLength': 743}, {'file': 'node_modules/piscina/benchmark/queue-comparison.js', 'sha256': '0f2a3d44e1f3c64b2cd7223c484b49bafb98a5c8dfd1fe34c6d327ebebc14910', 'byteLength': 758}, {'file': 'node_modules/piscina/benchmark/piscina-queue-comparison.js', 'sha256': '621d25cf2645f11be9df950199e43ab7b46a0f78cc8730f54a6484be8c6298d8', 'byteLength': 1093}, {'file': 'node_modules/piscina/dist/index.js', 'sha256': 'c47bd61dbafa0d5cada595709f0d688666402e8d53968659394fb76579c88b1f', 'byteLength': 33387}, {'file': 'node_modules/piscina/dist/worker.js', 'sha256': '081e85839bad17b8d895bae9e39ebb890933148185a9a547eb11cf4f3b410ceb', 'byteLength': 8248}, {'file': 'node_modules/piscina/dist/abort.js', 'sha256': '96bda77290b59f3f05f054fc67f8dd3c911c5b41615c767653f9c5be1100f181', 'byteLength': 690}, {'file': 'node_modules/piscina/dist/types.js', 'sha256': 'd6a24e27e1b415e8b9a6861d070c599c2fc71a86513c3a1b7d22c96798c9e14f', 'byteLength': 220}, {'file': 'node_modules/piscina/dist/errors.js', 'sha256': '8a1ba396c508b3981573e61d9d7c46c311a50f1a94891e3951b4a8a1e58d488d', 'byteLength': 551}, {'file': 'node_modules/piscina/dist/common.js', 'sha256': '7408b87975c0b2daf9f82f13b201f4e53b6f4a571ab1b4e997cbfe4204690695', 'byteLength': 3159}, {'file': 'node_modules/piscina/dist/main.js', 'sha256': '66553ddfb435a0093214eacb1c44971dc862e896f222ce9251cc6a9a15212bb6', 'byteLength': 272}, {'file': 'node_modules/piscina/dist/symbols.js', 'sha256': 'd0e30b1d318751183ffab01979895bc0fc45f1db6c6db36f83d58f2329f0011a', 'byteLength': 686}, {'file': 'node_modules/piscina/benchmark/fixtures/add.js', 'sha256': '48c3b9da5c294a707f11cfd562fe1092c68d50d52c124ac0f5305d81f874d726', 'byteLength': 52}, {'file': 'node_modules/piscina/dist/task_queue/index.js', 'sha256': 'e0bab14fc4f6e3e84adc1ed9d9190de1cda03af0894c21e7d8697382dd8741df', 'byteLength': 3723}, {'file': 'node_modules/piscina/dist/task_queue/common.js', 'sha256': 'efa0b2439294e1510ac01e9e254e9fbd509834ed9c4fdc73559c6b62f69a8264', 'byteLength': 113}, {'file': 'node_modules/piscina/dist/task_queue/array_queue.js', 'sha256': '29fe6991e3eab05119782880776adac3813e693b6c82ed8cbeab68296dca3921', 'byteLength': 862}, {'file': 'node_modules/piscina/dist/task_queue/fixed_queue.js', 'sha256': '626b1062fcfccd7568a349cd3ca2c0cb224dde65ddd98099d5b618d594c9eb10', 'byteLength': 6594}, {'file': 'node_modules/piscina/dist/worker_pool/index.js', 'sha256': 'b784db251a8f114d3ca0410468d25d05b1889050fbd266425bafc8d59664a867', 'byteLength': 6654}, {'file': 'node_modules/piscina/test/fixtures/send-buffer-then-get-length.js', 'sha256': 'b6646486752c157f6822ad178dea9bbf16c98611404afcaa773afc6ab61410f5', 'byteLength': 313}, {'file': 'node_modules/piscina/test/fixtures/vm.js', 'sha256': 'e1682e3cb237dbc16d19f28c0950d8978858d5b5d8b385997a8c5e963ac2e974', 'byteLength': 164}, {'file': 'node_modules/piscina/test/fixtures/resource-limits.js', 'sha256': '7dfd8f172c3328b7626399e2041a3d2c67a95bd5046a7a781628fa8a8342d7cf', 'byteLength': 109}, {'file': 'node_modules/piscina/test/fixtures/eval.js', 'sha256': 'ad103ef0038cda945182d95f2e8a610b44a1e46ef07771ced2e61b498567331a', 'byteLength': 93}, {'file': 'node_modules/piscina/test/fixtures/send-transferrable-then-get-length.js', 'sha256': 'fa8a63779d65ee6d41f329a948f9e75acb663c1956e0c0e84f1c11caf4985628', 'byteLength': 642}, {'file': 'node_modules/piscina/test/fixtures/eval-async.js', 'sha256': '01ff257a8f50fd0ae46531a7240dead492be9dae82c5cc0dd8537120c8a91cc8', 'byteLength': 267}, {'file': 'node_modules/piscina/test/fixtures/sleep.js', 'sha256': '53133a2a5ef9bd1ece0d0a3ea276cdf598d7cfcb6c295bcec2152819787fedf0', 'byteLength': 277}, {'file': 'node_modules/piscina/test/fixtures/multiple.js', 'sha256': '60c74291d42de9f7310de63c25a21d667ccf6b1dfe46adb87af80b70f4e7fabb', 'byteLength': 116}, {'file': 'node_modules/piscina/test/fixtures/notify-then-sleep-or.js', 'sha256': '1e3baabedf56a7af1cd2d1411334d9fe361bae91b0a87e64ffeb7704415e04bd', 'byteLength': 347}, {'file': 'node_modules/tree-sitter/jest-tests/parse_input.js', 'sha256': 'c822a080455cc0991ed04918a58a57b8ff736c62382155f3d868f27db489e800', 'byteLength': 243}, {'file': 'node_modules/tree-sitter/jest-tests/test3.test.js', 'sha256': 'dd89cb5d72894e26dd0535e326fbfc83582a5161a4b6970b8fa6bc17d117b3f3', 'byteLength': 624}, {'file': 'node_modules/tree-sitter/jest-tests/constants.js', 'sha256': 'da2f35a1728f8c6e376b87a66c9a1fc9f5c466f3103c38c01f43075f6081f138', 'byteLength': 657}, {'file': 'node_modules/tree-sitter/jest-tests/test.test.js', 'sha256': '9fb07c9ff5d3cc31298fd561f4f2f8828e713e0b75cb2208cbe204726c940716', 'byteLength': 5985}, {'file': 'node_modules/tree-sitter/jest-tests/runit.js', 'sha256': '61eabd4d64149552ff42b4f2dfe1827fef16aa104e33e5d18678e2af8852d494', 'byteLength': 298}, {'file': 'node_modules/tree-sitter/jest-tests/test2.test.js', 'sha256': 'ce68dd4056372dafa9617b7472098a3a50a9bbd61bc4792f35642263cc42f528', 'byteLength': 614}, {'file': 'node_modules/tree-sitter/test/lookahead_iterable_test.js', 'sha256': '7091be46ce22a6e76f322742e9b92d792e6916117ee1969fd39f80666db58083', 'byteLength': 1434}, {'file': 'node_modules/tree-sitter/test/parser_test.js', 'sha256': '6e9529e6d2213caf55bf8b8f4ad5293e376aad4759b836b96bcb4a20ad0def9c', 'byteLength': 24623}, {'file': 'node_modules/tree-sitter/test/node_test.js', 'sha256': '7dbf4cf445d47731fb5c7e283e74695ceb1221d5af0db465b7dabd52baf811d4', 'byteLength': 36780}, {'file': 'node_modules/tree-sitter/test/query_test.js', 'sha256': '407aed8a46d381128803ae22ce4e61441373f9a4bc0b4c9c9d61dc0fbb72f851', 'byteLength': 36549}, {'file': 'node_modules/tree-sitter/test/tree_test.js', 'sha256': 'ba487df54014f31dbf5e027bca465d4f891b7bc09943d55dbf686926ea79f838', 'byteLength': 19983}, {'file': 'node_modules/tree-sitter/vendor/tree-sitter/lib/src/wasm/stdlib-symbols.txt', 'sha256': '1162b06514d583b0e94fe9b59ddcc36257f587047cacf7944e87b01722109294', 'byteLength': 253}, {'file': 'node_modules/@jsquash/webp/index.js', 'sha256': '281f1d048db337aaa2412503423b2ca99eeb8ec104b471618ee3845930a27688', 'byteLength': 98}, {'file': 'node_modules/@jsquash/webp/package.json', 'sha256': 'bfd9f9b7ec0d51421623e92185b3b994d6430068de323dacaa1a29b8a1c9c83c', 'byteLength': 978}, {'file': 'node_modules/@jsquash/webp/decode.js', 'sha256': 'b15d9abe76ad4b07214739fe81a99a47f25d01a1e0b0a329a3bec706509cd9e2', 'byteLength': 1432}, {'file': 'node_modules/@jsquash/webp/encode.js', 'sha256': 'edcd883d10e955655946b8fe733bb139faa8b7036092827b1849a4b34d727d6b', 'byteLength': 1946}, {'file': 'node_modules/@jsquash/webp/utils.js', 'sha256': 'e71535eeee820d68b68ece7c8af761c39f3ddec93d3d957d2f83b19191339e0d', 'byteLength': 1272}, {'file': 'node_modules/@jsquash/webp/meta.js', 'sha256': '3d70163a00e6264fc5a8fa7c8f3b1fa21a50a00d910f4d2de3ef1bbd2fdc98ab', 'byteLength': 749}, {'file': 'node_modules/@jsquash/webp/codec/pre.js', 'sha256': 'b4d7da800804a2390eefa7805bb5f4e20cf349681334eb9a1715d2c58a1822b2', 'byteLength': 847}, {'file': 'node_modules/@jsquash/webp/codec/dec/webp_dec.js', 'sha256': 'c57971611f4d9ec04e4636ce7bb4a35c031b24cbdb013518f0a017d9f6014370', 'byteLength': 34823}, {'file': 'node_modules/@jsquash/webp/codec/enc/webp_enc.js', 'sha256': '5fd62301662e37785aec38e38807926f72933d4c8b919018a43faf1b1ca760f6', 'byteLength': 38665}, {'file': 'node_modules/@jsquash/webp/codec/enc/webp_enc_simd.js', 'sha256': '3038e60ebba6252baba08c691e31d1efe5036a185435daa7b4afaef3cc9273f9', 'byteLength': 38646}]
def strict_json(data):
    def pairs(values):
        result = dict(values); assert len(result) == len(values), 'duplicate JSON key'; return result
    return json.loads(data, object_pairs_hook=pairs, parse_constant=lambda _: (_ for _ in ()).throw(ValueError('nonfinite JSON')))

def append(data):
    fields = w.fields(data); numbers = [n for n,t,v in fields]
    assert len(numbers) == len(set(numbers)) and set(numbers) <= {1,2,3,4}, 'unknown/duplicate append field'
    assert 2 in numbers and (1 in numbers) != (4 in numbers), 'ambiguous/missing append payload'
    assert all(t == (0 if n == 3 else 2) for n,t,v in fields), 'append wire type'
    request = w.first(data,2); assert w.fields(request) == [(1,2,w.first(request,1))], 'append request identity'
    rid,seq,msg = w.decode_append(data); assert rid and 0 <= seq <= 127
    return rid,seq,msg

def classify(msg):
    fields = w.fields(msg); assert len(fields) == 1 and fields[0][1] == 2, 'unknown client envelope'
    if fields[0][0] == 5 and [n for n,t,v in w.fields(fields[0][2])] == [3]:
        return {'kind': 'heartbeat'}  # approved ExecClientControl heartbeat
    q = w.classify_client_message(msg)
    assert q['kind'] in ['run','stop-result','exec-close','heartbeat'], 'unknown native protocol'
    if q['kind'] == 'run':
        run = w.first(msg,1)
        q['requestedModel'] = w.first(w.first(run,9,b''),1,b'').decode()
        q['selectedModel'] = w.first(w.first(run,15,b''),1,b'').decode()
        assert q['requestedModel'] == q['selectedModel'] == 'TEST-native-stop'
        assert q['conversation'] and q['generation']
    if q['kind'] == 'stop-result':
        result = w.first(msg,2)
        assert len(w.fields(result)) == len({n for n,t,v in w.fields(result)})
        assert set(n for n,t,v in w.fields(result)) <= {1,15,27,39}, 'unknown Stop result'
        assert all(t == (2 if n in [15,27] else 0) for n,t,v in w.fields(result)), 'Stop result wire type'
        assert 0 <= w.first(result,39,0) <= 5000, 'local hook execution time'
        hook = w.first(result,27); assert w.fields(hook) == [(1,2,w.first(hook,1))]
        response = w.first(hook,1); assert w.fields(response) == [(11,2,b'')], 'nonneutral Stop response'
        assert q['id'] == 1 and q['execId'] == '' and q['hasStop'] and not q['followup']
    if q['kind'] == 'exec-close':
        assert msg == w.blob(5,w.blob(1,w.uint(1,1))), 'unexpected close'
    return q

def body(h):
    # r5 Connect unary framing.
    if h.headers.get('Transfer-Encoding', '').lower() == 'chunked':
        data = bytearray()
        while True:
            size = int(h.rfile.readline(128).split(b';')[0], 16)
            if not size:
                assert h.rfile.readline(128) == b'\r\n'
                break
            assert 0 <= size <= LIMIT-len(data)
            data.extend(h.rfile.read(size)); assert h.rfile.read(2) == b'\r\n'
        data = bytes(data)
    else:
        size = int(h.headers.get('Content-Length', '0'))
        assert 0 <= size <= LIMIT
        data = h.rfile.read(size); assert len(data) == size
    if h.headers.get('Content-Encoding') == 'gzip':
        with gzip.GzipFile(fileobj=io.BytesIO(data)) as f: data = f.read(LIMIT+1)
    assert len(data) <= LIMIT
    return data

def own_processes():
    result = []
    for p in pathlib.Path('/proc').glob('[0-9]*/stat'):
        try:
            fields = p.read_text().rsplit(')', 1)[1].split()
            result.append((int(p.parent.name), int(fields[1]), int(fields[2]), int(fields[21])*os.sysconf('SC_PAGE_SIZE'), int(fields[3]), int(fields[19])))
        except (FileNotFoundError, ProcessLookupError): pass
    return result

def owned_survivors(procs, leader, controller, namespace):
    # stat; no names/env.
    assert os.getpid() == controller and os.readlink('/proc/self/ns/pid') == namespace
    assert any(p[0] == controller for p in procs), 'controller absent from snapshot'
    assert leader[0] == leader[2] == leader[4] and leader[0] != os.getpgrp(), 'unique owned group'
    children = [p for p in procs if p[0] != controller]
    ids = {p[0] for p in children} | {controller, leader[0]}
    for p in children:
        assert p[2] == p[4] == leader[0] and p[5] >= leader[5], 'unexpected outgroup/identity'
        assert p[1] in ids, 'unexpected descendant parent'
        if p[0] == leader[0]: assert p[5] == leader[5], 'native leader identity changed'
        try: assert os.readlink('/proc/'+str(p[0])+'/ns/pid') == namespace, 'PID namespace changed'
        except FileNotFoundError: pass  # exited during fresh observation; join still required
    assert len(children) <= 65 and sum(p[3] for p in children) <= 4*1024**3, 'native process/RSS budget'
    assert len(procs) <= 72 and sum(p[3] for p in procs) <= 5*1024**3, 'owned namespace process/RSS budget'
    return children

def request_owned_cleanup(procs, leader, controller, namespace, deadline, sig=signal.SIGTERM):
    children = owned_survivors(procs, leader, controller, namespace)
    assert time.monotonic() < deadline, 'cleanup deadline'
    for p in children:
        try:
            assert os.getpgid(p[0]) == os.getsid(p[0]) == leader[0], 'fresh group identity'
        except ProcessLookupError: continue
    assert time.monotonic() < deadline, 'cleanup deadline'
    if not children: return False
    try: os.killpg(leader[0], sig); return True
    except ProcessLookupError: return False

def join_owned(native, leader, controller, namespace, deadline, snapshot=own_processes, budget=lambda: None):
    while time.monotonic() < deadline:
        native.poll()
        try:
            while os.waitpid(-1, os.WNOHANG)[0]: pass
        except ChildProcessError: pass
        children = owned_survivors(snapshot(), leader, controller, namespace); budget()
        if not children: return time.monotonic() < deadline
        time.sleep(min(.1, max(0, deadline-time.monotonic())))
    return False

def cmdline_facts(raw):
    args = raw[:8192].decode('utf8','surrogateescape').split('\0')
    if args[-1] == '': args.pop()
    return {'argv':args, 'cmdlineBytes':len(raw), 'argvOverflow':len(raw)>8192,
            'argvMalformed':bool(raw) and not raw.endswith(b'\0')}

def process_snapshot(procs, namespace):
    # Bound facts; no env/auth.
    rows = []
    for p in procs[:73]:
        row = {'stat':p}; proc = pathlib.Path('/proc',str(p[0])); stage = 'cmdline'
        try:
            with (proc/'cmdline').open('rb') as f: raw = f.read(8193)
            row.update(cmdline_facts(raw))
            links = {}
            for field,link in (('exe','exe'),('cwd','cwd'),('namespace','ns/pid'),('netNamespace','ns/net')):
                stage = field
                links[field] = os.readlink(proc/link)
            row.update(links)
            stage = 'status'
            with (proc/'status').open() as f: status = f.read(8193)
            row['uid'] = next(l for l in status.splitlines() if l.startswith('Uid:')).split()[1:]
            stage = 'stat'
            with (proc/'stat').open() as f: fields = f.read(8193).rsplit(')',1)[1].split()
            row['freshIdentity'] = [int(fields[i]) for i in [1,2,3,19]]
        except (OSError,ValueError,StopIteration) as e:
            row.update(readFailure=type(e).__name__,refusalStage=stage)
            try:
                with (proc/'stat').open() as f: raw = f.read(8193)
                fields = raw.rsplit(')',1)[1].split()
                row['failureStat'] = {'pid':raw.split('(',1)[0].strip()[:32],
                    'birth':fields[19][:32],'state':fields[0][:1]}
            except (OSError,ValueError,IndexError) as diagnostic_error:
                row['failureStatReadFailure'] = type(diagnostic_error).__name__
        if len(json.dumps(row).encode()) > 12000:
            row = {'stat':p,'readFailure':'ProcessMetadataBudget', **{k:str(row.get(k,''))[:64] for k in ['exe','cwd','namespace','uid']},
                   'argv':[a[:64] for a in row.get('argv',[])[:8]]}
        rows.append(row)
    return {'namespace':namespace,'totalProcesses':len(procs),'totalRSS':sum(p[3] for p in procs),
            'bornUpperTicks':int(time.clock_gettime(time.CLOCK_BOOTTIME)*os.sysconf('SC_CLK_TCK')),
            'netNamespace':os.readlink('/proc/self/ns/net'),'rows':rows}

def capture_active(evidence, procs, namespace, phase, previous, checks, birth_floor):
    snapshot = process_snapshot(procs,namespace)
    snapshot.update(phase=phase,previous={str(k):v for k,v in previous.items()},checks=checks,
                    birthFloorTicks=birth_floor,ownershipContract='native-descendants')
    raw = json.dumps(snapshot,indent=2); assert len(raw.encode()) <= LIMIT, 'actual snapshot bytes budget'
    name = 'active-process-snapshot.json' if phase == 'active' else 'cleanup-actual-process-snapshot.json'
    (evidence/name).write_text(raw+'\n'); return snapshot

class ProcessObservationRace(AssertionError): pass

def active_resource_bounds(snapshot):
    rows = snapshot['rows']; children = [r for r in rows if r['stat'][0] != 1]
    # Bound totals/incomplete; no retry.
    assert all(len(r['stat']) == 6 and r['stat'][3] >= 0 for r in rows), 'invalid process RSS'
    assert snapshot['totalProcesses'] <= 72 and snapshot['totalRSS'] <= 5*1024**3, 'namespace process/RSS budget'
    assert len(children) <= 65 and sum(r['stat'][3] for r in children) <= 4*1024**3, 'descendant process/RSS budget'
    assert len(rows) == snapshot['totalProcesses'] and snapshot['totalRSS'] == sum(r['stat'][3] for r in rows), 'incomplete process totals'

def complete_process_facts(snapshot, namespace):
    race = None
    for row in snapshot['rows']:
        p = row['stat']
        if 'uid' in row: assert row['uid'] == ['1000']*4, 'foreign UID'
        if 'namespace' in row: assert row['namespace'] == namespace, 'foreign PID namespace'
        if 'netNamespace' in row: assert row['netNamespace'] == snapshot['netNamespace'], 'foreign network namespace'
        assert not row.get('argvOverflow',False) and not row.get('argvMalformed',False), 'overflow/malformed actual cmdline'
        if row.get('readFailure') in ['FileNotFoundError','ProcessLookupError']:
            race = ProcessObservationRace('process exited during metadata snapshot'); continue
        assert 'readFailure' not in row and not row['argvOverflow'], 'incomplete actual process facts'
        assert row['uid'] == ['1000']*4 and row['namespace'] == namespace
        assert 0 <= row['cmdlineBytes'] <= 8192, 'actual cmdline byte budget'
        if row['freshIdentity'] != [p[1],p[2],p[4],p[5]]:
            assert row['freshIdentity'][3] == p[5], 'PID reused during metadata snapshot'
            race = ProcessObservationRace('process changed during metadata snapshot')
        if row['cmdlineBytes'] == 0:
            assert row['argv'] == [], 'inconsistent empty actual cmdline'
            race = ProcessObservationRace('empty cmdline during metadata snapshot')
    if race: raise race

def active_descendants(snapshot, leader, namespace, root, previous, birth_floor, deadline, cwd_allowances=None):
    active_resource_bounds(snapshot)
    assert time.monotonic() < deadline, 'native runtime budget'
    rows = snapshot['rows']; bypid = {r['stat'][0]:r for r in rows}
    assert snapshot['namespace'] == namespace and len(bypid) == len(rows), 'foreign namespace/duplicate PID'
    assert 1 in bypid and leader[0] == leader[2] == leader[4] > 1, 'unique native leader'
    assert birth_floor <= leader[5] <= snapshot['bornUpperTicks'], 'fresh native leader birth'
    identities = dict(previous)
    for row in rows:
        p = row['stat']; old = previous.get(p[0])
        assert len(p) == 6 and p[0] > 0 and p[3] >= 0 and p[5] > 0, 'invalid process stat'
        assert not old or old['start'] == p[5], 'PID start identity changed'
        if 'uid' in row: assert row['uid'] == ['1000']*4, 'foreign UID'
        if 'namespace' in row: assert row['namespace'] == namespace, 'foreign PID namespace'
        if p[0] != 1:
            assert leader[5] <= p[5] <= snapshot['bornUpperTicks'], 'monotonic process birth bound'
            if 'cwd' in row: assert row['cwd'] == (cwd_allowances or {}).get((p[0],p[5]),str(root/'workspace')), 'foreign process cwd'
        identities[p[0]] = {'start':p[5]}
    assert bypid[1]['stat'][1] == 0, 'private namespace controller'
    assert leader[0] in bypid, 'native leader missing'
    observed = bypid[leader[0]]['stat']
    assert observed[1] == 1 and observed[2] == observed[4] == leader[0] and observed[5] == leader[5], 'native leader identity'
    for pid in bypid.keys()-{1}:
        cursor = pid; visited = set()
        while cursor != leader[0]:
            assert cursor in bypid and cursor not in visited, 'disconnected/cyclic active ancestry'
            visited.add(cursor); p = bypid[cursor]['stat']; parent = p[1]
            if parent == 1:
                assert previous.get(cursor) == {'start':p[5]}, 'unobserved controller adoption'
                break
            assert parent in bypid and bypid[parent]['stat'][5] <= p[5], 'foreign/reused active parent'
            cursor = parent
    complete_process_facts(snapshot,namespace)
    return identities  # retain validated PID/start pairs across exits and shell/Node execs

def active_namespace(snapshot, network, root):
    assert snapshot['netNamespace'] == network, 'controller network namespace changed'
    assert all(r['netNamespace'] == network for r in snapshot['rows']), 'foreign network namespace'
    assert next(r for r in snapshot['rows'] if r['stat'][0] == 1)['cwd'] == str(root/'workspace'), 'controller TEST cwd'

def record_refusal(evidence, snapshot, error):
    raw = (json.dumps({'failure':type(error).__name__+':'+str(error),'snapshot':snapshot})+'\n').encode()
    target = evidence/'active-process-refusals.jsonl'
    assert len(raw)+(target.stat().st_size if target.exists() else 0) <= 16*LIMIT, 'refusal diagnostics budget'
    with target.open('ab') as f: f.write(raw)

def observe_owned_active(evidence, leader, namespace, network, root, previous, checks, birth_floor, deadline, parent, installed=None):
    observed_previous = dict(previous)
    for attempt in range(32):
        snapshot = capture_active(evidence,own_processes(),namespace,'active',observed_previous,checks,birth_floor)
        try:
            active_resource_bounds(snapshot)
            assert time.monotonic() < deadline, 'native runtime budget'
            for row in snapshot['rows']:
                p = row['stat']; old = observed_previous.get(p[0])
                assert not old or old['start'] == p[5], 'PID start identity changed during fresh observations'
                if not old: observed_previous[p[0]] = {'start':p[5], 'validated':False}
            if parent.poll() is not None: return snapshot, observed_previous  # unchanged strict postparent checks
            identities = active_descendants(snapshot,leader,namespace,root,observed_previous,birth_floor,deadline, native_cwd_allowances(snapshot,root,installed) if installed else None)
            active_namespace(snapshot,network,root)
            (evidence/'last-validated-active-process-snapshot.json').write_text(json.dumps(snapshot,indent=2)+'\n')
            return snapshot, identities
        except Exception as e:
            record_refusal(evidence,snapshot,e)
            if not isinstance(e,ProcessObservationRace) or attempt == 31: raise
            assert time.monotonic() < deadline, 'native runtime budget'
            time.sleep(min(.01,max(0,deadline-time.monotonic())))


def observe_cleanup_facts(evidence, namespace, network, root, previous, checks, birth_floor, deadline):
    for attempt in range(4):
        snapshot = capture_active(evidence,own_processes(),namespace,'after-natural-before-strict-cleanup',previous,checks,birth_floor)
        try:
            active_resource_bounds(snapshot)
            assert time.monotonic() < deadline, 'cleanup deadline'
            complete_process_facts(snapshot,namespace); active_namespace(snapshot,network,root)
            return snapshot
        except Exception as e:
            record_refusal(evidence,snapshot,e)
            if not isinstance(e,ProcessObservationRace) or attempt == 3: raise
            assert time.monotonic() < deadline, 'cleanup deadline'
            time.sleep(min(.01,max(0,deadline-time.monotonic())))


def validate_stop(root,run):
    evidence = root/'evidence'; record = strict_json((evidence/'native-stop.json').read_text())
    assert set(record) == {'event','ctxActive','cwd','home','pid','ppid'}, 'unknown SDK callback output'
    event = {'hook_event_name':'stop','status':'completed','conversation_id':run['conversation'],
             'generation_id':run['generation'],'model':'TEST-native-stop','model_id':'TEST-native-stop',
             'workspace_roots':[str(root/'workspace')],'cursor_version':'2026.09.28-64d2043',
             'loop_count':0,'transcript_path':None,'user_email':''}
    assert json.dumps(record['event'],sort_keys=True) == json.dumps(event,sort_keys=True) and record['ctxActive'] is True
    assert record['home'] == str(root/'home') and record['cwd'] == str(root/'workspace')
    facts = strict_json((evidence/'native-fd-facts.json').read_text())
    assert record['pid'] == facts['observerPid'] and record['ppid'] == facts['probePid']
    for fd in ['0','1','2']:
        assert facts[fd]['socket'] and not facts[fd]['pipe'] and facts[fd]['domain'] == 1 and facts[fd]['socketType'] == 1

def validate_output(evidence,run):
    output = strict_json((evidence/'native.stdout').read_text())
    assert set(output) == {'type','subtype','is_error','result','session_id','request_id','duration_ms','duration_api_ms'}, 'unknown native output'
    assert all(type(output[k]) is int and 0 <= output[k] <= 90000 for k in ['duration_ms','duration_api_ms'])
    assert output['type'] == 'result' and output['subtype'] == 'success' and output['is_error'] is False
    assert output['result'] == 'TEST deterministic turn complete'
    assert output['session_id'] == run['conversation'] and output['request_id'] == run['requestId']
    assert (evidence/'native.stderr').read_bytes() == b'', 'native stderr'

def worker_roles(children,artifact,root,namespace):
    roles = []
    for p in children:
        try:
            proc = pathlib.Path('/proc',str(p[0]))
            with (proc/'cmdline').open('rb') as f: raw = f.read(4097)
            assert len(raw) <= 4096, 'owned argv budget'
            assert raw == b'\0'.join([str(artifact/'node').encode(),str(artifact/'index.js').encode(),b'worker-server',b''])
            assert os.readlink(proc/'exe') == str(artifact/'node') and os.readlink(proc/'cwd') == str(root/'workspace')
            assert os.readlink(proc/'ns/pid') == namespace
            assert next(l for l in (proc/'status').read_text().splitlines() if l.startswith('Uid:')).split()[1:] == ['1000']*4
            fresh = next(q for q in own_processes() if q[0] == p[0]); assert fresh[2] == p[2] and fresh[4:] == p[4:], 'fresh worker identity'
            roles.append({'pid':p[0],'workerServerArgvMatches':True,'executableIsPinnedNode':True,
                          'cwdMatches':True,'uid':1000,'startTicks':p[5],'pgid':p[2],'sid':p[4]})
        except (FileNotFoundError,ProcessLookupError,StopIteration):
            roles.append({'pid':p[0],'exitedBeforeRoleSnapshot':True})
    return roles

class IncompleteInstalledContract(RuntimeError): pass


class CaseClock:
    """One outer/work deadline and eight seconds of cumulative joining work."""
    def __init__(self, outer_deadline):
        self.outer = outer_deadline
        self.work = None
        self.cleanup_used = 0.0

    def native_started(self):
        if self.work is None:
            self.work = min(time.monotonic()+90, self.outer-8)
        self.wait(90)

    def wait(self, local_cap, cleanup=False):
        end = self.outer if cleanup else min(self.outer-8, self.work or self.outer-8)
        remaining = end-time.monotonic()
        if cleanup:
            remaining = min(remaining, 8-self.cleanup_used)
        assert remaining > 0, 'original absolute/cumulative deadline exhausted'
        return min(local_cap, remaining)

    def cleanup_deadline(self):
        return time.monotonic()+self.wait(8, cleanup=True)

    def charge_cleanup(self, began):
        self.cleanup_used += time.monotonic()-began
        assert self.cleanup_used <= 8 and time.monotonic() < self.outer, 'cumulative cleanup allowance'


def startup_identity(pid, expected=None):
    """Provisional own-child tuple; never full executable qualification."""
    def sample():
        raw = (pathlib.Path('/proc')/str(pid)/'stat').read_text()
        tail = raw[raw.rindex(')')+2:].split()
        return {'pid':int(raw[:raw.index('(')].strip()),'birth':int(tail[19]),
                'ppid':int(tail[1]),'pgid':int(tail[2]),'sid':int(tail[3]),'startup':True}
    facts = sample()
    assert facts == sample(), 'startup tuple race'
    assert facts['pid'] == pid and facts['ppid'] == os.getpid() and facts['pgid'] == facts['sid'] == pid, 'own startup child tuple'
    if expected is not None:
        assert facts == expected, 'startup tuple changed'
    return facts


def proc_identity(pid, expected=None, startup=False, leader_parent=None):
    """Ancestor-visible identities; no inferred birth or PID-only ownership."""
    p = pathlib.Path('/proc')/str(pid)
    raw = (p/'stat').read_text()
    tail = raw[raw.rindex(')')+2:].split()
    status = {}
    for line in (p/'status').read_text().splitlines():
        key, sep, value = line.partition(':')
        if sep: status[key] = value.strip()
    cmdline = (p/'cmdline').read_bytes()
    if startup:
        assert expected is not None and expected.get('startup') is True, 'startup sample requires retained tuple'
        assert int(tail[19]) == expected['birth'] and int(tail[1]) == expected['ppid'] and int(tail[2]) == expected['pgid'] and int(tail[3]) == expected['sid'], 'startup sampled tuple changed'
        startup_identity(pid,expected)
        if cmdline == b'':
            assert tail[0] not in ('Z','X','x'), 'startup process terminal'
            return None
    immutable = ('pid','birth','ppid','tgid','uids','gids','groups','nspid','tracer','ns','uidMap','gidMap')
    if leader_parent is not None and cmdline == b'':
        fresh = trace_task_identity(pid)
        assert expected is not None and all(fresh[key] == expected[key] for key in immutable), 'leader observation authority drift'
        assert tail[0] not in ('Z','X','x') and fresh['ppid'] == leader_parent and fresh['pgid'] == fresh['sid'] == pid, 'leader observation parent/session/terminal'
        assert (int(raw[:raw.index('(')].strip()),int(tail[19]),int(tail[1]),int(tail[2]),int(tail[3])) == tuple(fresh[key] for key in ('pid','birth','ppid','pgid','sid')), 'leader observation stat bookend'
        assert int(status['Tgid']) == fresh['tgid'] and int(status['TracerPid']) == fresh['tracer'] and all(list(map(int,status[field].split())) == fresh[key] for field,key in (('Uid','uids'),('Gid','gids'),('Groups','groups'),('NSpid','nspid'))), 'leader observation status bookend'
        raise ProcessObservationRace('empty selected leader cmdline observation')
    if not (0 < len(cmdline) <= 8192 and cmdline.endswith(b'\0')):
        try:
            print('argv-budget-refusal', {'pid':int(pid), 'cmdlineBytes':len(cmdline),
                  'endsNUL':cmdline.endswith(b'\0'), 'state':tail[0][:1], 'birth':tail[19][:32],
                  'expectedBirth':str(expected.get('birth',''))[:32] if expected is not None else None},
                  file=sys.stderr,flush=True)
        except Exception:
            pass
    assert 0 < len(cmdline) <= 8192 and cmdline.endswith(b'\0'), 'actual argv budget'
    facts = {'pid':int(pid), 'birth':int(tail[19]), 'ppid':int(tail[1]),
             'pgid':int(tail[2]), 'sid':int(tail[3]), 'tgid':int(status['Tgid']),
             'uids':list(map(int,status['Uid'].split())), 'gids':list(map(int,status['Gid'].split())),
             'groups':list(map(int,status['Groups'].split())), 'nspid':list(map(int,status['NSpid'].split())),
             'tracer':int(status['TracerPid']), 'argv':[v.decode('utf-8','strict') for v in cmdline[:-1].split(b'\0')],
             'exe':os.readlink(p/'exe'), 'cwd':os.readlink(p/'cwd'),
             'ns':{k:os.readlink(p/'ns'/k) for k in ('pid','net','user')},
             'uidMap':(p/'uid_map').read_text(), 'gidMap':(p/'gid_map').read_text()}
    assert (p/'stat').read_text().split(') ')[-1].split()[19] == str(facts['birth']), 'birth race'
    if expected is not None:
        assert facts['pid'] == expected['pid'] and facts['birth'] == expected['birth'], 'PID reuse'
    if leader_parent is not None:
        assert expected is not None and all(facts[key] == expected[key] for key in immutable), 'leader complete authority drift'
    return facts


def trace_task_identity(pid):
    """Fresh generic lineage metadata, never selected executable qualification."""
    p = pathlib.Path('/proc')/str(pid)
    def sample():
        raw = (p/'stat').read_text()
        tail = raw[raw.rindex(')')+2:].split()
        return (int(raw[:raw.index('(')].strip()),int(tail[19]),int(tail[1]),int(tail[2]),int(tail[3])),tail[0]
    before,state = sample()
    assert before[0] == pid and state not in ('Z','X','x'), 'live trace task required'
    status = {}
    for line in (p/'status').read_text().splitlines():
        key,sep,value = line.partition(':')
        if sep: status[key] = value.strip()
    facts = {'pid':before[0], 'birth':before[1], 'ppid':before[2],
             'pgid':before[3], 'sid':before[4], 'tgid':int(status['Tgid']),
             'uids':list(map(int,status['Uid'].split())), 'gids':list(map(int,status['Gid'].split())),
             'groups':list(map(int,status['Groups'].split())), 'nspid':list(map(int,status['NSpid'].split())),
             'tracer':int(status['TracerPid']), 'ns':{k:os.readlink(p/'ns'/k) for k in ('pid','net','user')},
             'uidMap':(p/'uid_map').read_text(), 'gidMap':(p/'gid_map').read_text()}
    after,state = sample()
    assert after == before and state not in ('Z','X','x'), 'trace task metadata race'
    return facts


def decoded_hex_string(token):
    assert re.fullmatch(r'"(?:\\x[0-9a-fA-F]{2})*"', token), 'abbreviated/unknown strace string'
    return bytes.fromhex(token[1:-1].replace('\\x',''))


def captured_stop_bytes(raw):
    assert type(raw) is bytes and 0 < len(raw) <= LIMIT, 'native payload budget'
    text = raw.decode('utf-8', 'strict')
    def pairs(items):
        result = {}
        for key, value in items:
            assert key not in result, 'duplicate native JSON key'
            result[key] = value
        return result
    def constant(value): raise AssertionError('nonfinite JSON number')
    value, end = json.JSONDecoder(object_pairs_hook=pairs,parse_constant=constant).raw_decode(text)
    assert re.fullmatch(r'[ \t\r\n]*',text[end:]), 'trailing non-JSON native payload bytes'
    assert type(value) is dict and value.get('hook_event_name') == 'stop', 'actual Cursor Stop required'
    for key in ('conversation_id','generation_id'):
        s = value.get(key)
        assert type(s) is str and 0 < len(s.encode('utf-8')) <= 256, 'native ID bounds'
        assert not any(unicodedata.category(c) == 'Cc' for c in s), 'native ID control character'
    assert value.get('status') in ('completed','aborted','error'), 'unknown stopping status'
    count = value.get('loop_count')
    assert count is None or type(count) is int and 0 <= count <= 2147483647, 'native loop count'
    return {'conversation':value['conversation_id'], 'generation':value['generation_id'],
            'status':value['status'], 'loopCountKnown':count is not None, 'loopCount':count or 0}


def stdin_endpoint_facts(link, mode, inode, unix):
    """Passive /proc endpoint qualification; Node pipes may be Unix sockets."""
    match = re.fullmatch(r'(pipe|socket):\[(\d+)\]',link)
    assert match and int(match[2]) == inode, 'stdin endpoint inode/link mismatch'
    if match[1] == 'pipe':
        assert stat.S_ISFIFO(mode), 'stdin pipe mode mismatch'
        return {'kind':'pipe','link':link,'inode':inode,'mode':mode}
    assert stat.S_ISSOCK(mode) and len(unix) <= LIMIT, 'stdin socket mode/table bound'
    rows = []
    for line in unix.decode('ascii','strict').splitlines()[1:]:
        fields = line.split(maxsplit=7)
        assert len(fields) >= 7, 'unknown Unix socket table row'
        if fields[6] == str(inode): rows.append(fields)
    assert len(rows) == 1 and rows[0][4] == '0001' and rows[0][5] == '03', 'stdin is not a proven connected AF_UNIX SOCK_STREAM'
    return {'kind':'socket','link':link,'inode':inode,'mode':mode,
            'family':'AF_UNIX','type':'SOCK_STREAM','state':'connected'}


def live_stdin_endpoint(tid):
    proc = pathlib.Path('/proc')/str(tid)
    fd = proc/'fd/0'
    link = os.readlink(fd); first = fd.stat()
    unix = b''
    if link.startswith('socket:'):
        with (proc/'net/unix').open('rb') as table: unix = table.read(LIMIT+1)
    result = stdin_endpoint_facts(link,first.st_mode,first.st_ino,unix)
    last = fd.stat()
    assert os.readlink(fd) == link and (first.st_mode,first.st_dev,first.st_ino) == (last.st_mode,last.st_dev,last.st_ino), 'stdin endpoint replacement during qualification'
    result['device'] = first.st_dev
    return result


class StdinAliasTrace:
    """Consume ordinary -xx syscall text. Only selected helper evidence survives.

    A task key is (outer TID, actual birth). Shared clone tables have reference
    identity; copied tables do not. Exec unshares and removes CLOEXEC descriptors.
    No descriptor number is a lineage fact until a selected live exec and dup(0).
    """
    MAX_STREAM = 512*LIMIT
    MAX_LINE = 5*LIMIT  # strace's \xHH output expands each byte to four ASCII bytes.
    MAX_EVENTS = 1000000
    def __init__(self, identity, helper_argv, helper_hash, controller, tracer, root):
        self.identity = identity
        self.helper_argv = helper_argv
        self.helper_hash = helper_hash
        self.controller = controller
        self.tracer = tracer
        self.root = pathlib.Path(root)
        self.tasks = {}
        self.pending = {}
        self.pending_reads = {}
        self.selected = []
        self.total = self.events = 0
        self.fragment = b''
        self.error = None
        self.checkpoints = set()
        self.clone_edges = {}
        self.remap_history = {}
        self.lock = threading.RLock()
        self.changed = threading.Condition(self.lock)

    def held_clone(self, parent_tid, child_tid, parent_birth, child_birth, raw_flags):
        """Admit only the actual paired kernel stops, before either resumes."""
        parent = self.register(parent_tid)
        assert parent['key'] == (parent_tid,parent_birth), 'held parent birth'
        prefix = self.pending.get(parent_tid,'')
        assert prefix.startswith(('clone(','clone3(','fork(','vfork(')), 'held clone entry absent'
        flags = set(re.findall(r'\bCLONE_[A-Z0-9_]+\b',prefix))
        assert ('CLONE_FILES' in flags) == bool(raw_flags & 0x400), 'held CLONE_FILES mismatch'
        assert ('CLONE_THREAD' in flags) == bool(raw_flags & 0x10000), 'held CLONE_THREAD mismatch'
        fresh = trace_task_identity(child_tid)
        assert fresh['birth'] == child_birth, 'held child birth'
        outer = self.namespace_child(fresh['nspid'][-1],parent,flags,child_tid)
        child = self.tasks[outer]
        assert child.get('parent') is None and not child.get('capture'), 'held child already admitted'
        for field in ('uids','gids','groups','tracer','ns','uidMap','gidMap'):
            assert fresh[field] == parent['facts'][field], 'held child authority/inheritance'
        fd_proof = self.clone_fds(parent_tid,child_tid,flags)
        child['parent'] = parent['key']
        child['fds'] = parent['fds'] if 'CLONE_FILES' in flags else copy.deepcopy(parent['fds'])
        capture = parent.get('capture')
        if capture: assert {'CLONE_FILES','CLONE_THREAD'} <= flags, 'unexpected selected helper fork/FD unshare'
        if 'CLONE_THREAD' in flags: child['capture'] = capture
        assert parent['key'] not in self.clone_edges, 'unreconciled clone edge'
        self.clone_edges[parent['key']] = {'key':child['key'],'local':fresh['nspid'][-1],
                                            'flags':flags,'prefix':prefix,'fds':fd_proof}
        assert trace_task_identity(parent_tid) == parent['facts'], 'held parent drift'
        assert trace_task_identity(child_tid) == fresh, 'held child drift'
        for recorded in child.pop('earlyChildCalls',[]): self.line(str(child_tid)+' '+recorded)

    def clone_fds(self, parent_tid, child_tid, flags):
        assert platform.machine() == 'x86_64', 'qualified Linux clone/table ABI'
        libc = ctypes.CDLL(None,use_errno=True)
        same_table = libc.syscall(312,parent_tid,child_tid,2,0,0)  # Linux/amd64 kcmp(KCMP_FILES).
        assert same_table >= 0 and (same_table == 0) == ('CLONE_FILES' in flags), 'actual clone FD table sharing'
        def fd_numbers(tid):
            values = {int(p.name) for p in (pathlib.Path('/proc')/str(tid)/'fd').iterdir()}
            assert len(values) <= 4096, 'held inheritance FD cap'
            return values
        inherited_fds = fd_numbers(parent_tid)
        assert fd_numbers(child_tid) == inherited_fds and set(self.tasks[parent_tid]['fds']) <= inherited_fds, 'held complete FD inheritance'
        proof = {}
        for fd in inherited_fds:
            def endpoint(tid):
                entry = pathlib.Path('/proc')/str(tid)/'fd'/str(fd)
                before = entry.stat(); link = os.readlink(entry); after = entry.stat()
                assert (before.st_dev,before.st_ino,before.st_mode) == (after.st_dev,after.st_ino,after.st_mode), 'inherited fd replacement'
                info = (pathlib.Path('/proc')/str(tid)/'fdinfo'/str(fd)).read_text()
                flags_at_stop = re.search(r'^flags:\s+([0-7]+)$',info,re.M)
                assert flags_at_stop, 'inherited fd flags'
                return (link,before.st_dev,before.st_ino,before.st_mode,int(flags_at_stop[1],8))
            before = endpoint(parent_tid)
            assert endpoint(child_tid) == before == endpoint(parent_tid), 'actual held FD inheritance'
            proof[fd] = before
        assert fd_numbers(parent_tid) == fd_numbers(child_tid) == inherited_fds, 'inherited FD set drift'
        return proof

    def task_for_key(self, key):
        task = self.tasks.get(key[0])
        if task is None or task['key'] != key: task = self.remap_history.get(key)
        assert task is not None and task['key'] == key, 'unknown historical task birth'
        return task

    def exec_remap(self, tid, old_tid):
        old = self.tasks[old_tid]; leader = self.tasks[tid]
        assert old_tid != tid and old['facts']['tgid'] == tid, 'actual exec TID remap group'
        assert not old.get('capture') and not leader.get('capture') and not leader.get('nativeLeader'), 'selected helper/leader unexpected thread exec'
        prefix = self.pending.pop(old_tid,'')
        assert prefix.startswith(('execve(','execveat(')), 'remap without admitted exec entry'
        facts = trace_task_identity(tid)
        for field in ('uids','gids','groups','tracer','ns','uidMap','gidMap'):
            assert facts[field] == old['facts'][field], 'fresh exec-remap authority'
        assert facts['tgid'] == tid and facts['nspid'] == leader['facts']['nspid'] and facts['ppid'] == leader['facts']['ppid'], 'fresh exec-remap local/parent mapping'
        for task in (old,leader): self.remap_history[task['key']] = dict(task)
        remapped = dict(old,key=(tid,facts['birth']),facts=facts,parent=leader['parent'])
        remapped.pop('exited',None)
        remapped['execRemap'] = {'old':old['key'],'superseded':leader['key'],'fresh':remapped['key']}
        old['exited'] = True
        leader['exited'] = True
        leader['supersededPending'] = self.pending.pop(tid,None)
        assert tid not in self.pending_reads, 'exec remap cannot cancel selected read custody'
        self.tasks[tid] = remapped
        self.pending[tid] = prefix
        assert trace_task_identity(tid) == facts, 'exec-remap bookend'

    def bus_fd(self, tid, fd, connected=False):
        proc = pathlib.Path('/proc')/str(tid)
        entry = proc/'fd'/str(fd)
        link = os.readlink(entry); before = entry.stat()
        match = re.fullmatch(r'socket:\[(\d+)\]',link)
        assert match and stat.S_ISSOCK(before.st_mode) and int(match[1]) == before.st_ino, 'actual bus socket fd'
        with (proc/'net/unix').open('rb') as table: raw = table.read(LIMIT+1)
        assert len(raw) <= LIMIT, 'bus socket table cap'
        rows = [row.split() for row in raw.decode('ascii').splitlines()[1:]]
        rows = [row for row in rows if len(row) >= 7 and row[6] == str(before.st_ino)]
        assert len(rows) == 1 and rows[0][4] == '0001', 'fresh AF_UNIX stream socket'
        if connected: assert rows[0][5] == '03', 'tracked bus socket is not freshly connected'
        after = entry.stat()
        assert os.readlink(entry) == link and (before.st_dev,before.st_ino,before.st_mode) == (after.st_dev,after.st_ino,after.st_mode), 'bus fd replacement'
        return (before.st_dev,before.st_ino)

    def held_entry(self, task, name, arguments, session):
        """Closed connect intent and destruction; existing FD ledger is authority."""
        capture = task.get('capture')
        if not capture:
            assert not any(value.get('bus') for value in task['fds'].values()), 'unselected table cannot own watched socket authority'
            return None
        self.register(task['key'][0])
        prefix = self.pending.get(task['key'][0],'')
        assert prefix.startswith(name+'('), 'held entry lacks its actual decoded prefix'
        unsupported = ('unshare','setns','pidfd_getfd','io_uring_setup','io_uring_enter','io_uring_register',
                       'setuid','setgid','setreuid','setregid','setresuid','setresgid','setgroups','capset')
        assert name not in unsupported and name not in ('execve','execveat'), 'selected descriptor/identity bypass'
        connections = capture.setdefault('busConnections',[])
        if name == 'connect' and 'sa_family=AF_UNIX' in prefix:
            match = re.search(r'sun_path=("(?:\\x[0-9a-fA-F]{2})*")',prefix)
            assert match, 'complete real AF_UNIX connect address'
            address = decoded_hex_string(match[1])
            assert address == str(session.directory/'bus').encode(), 'selected AF_UNIX connect outside owned bus address'
            session.assert_continuous()
            assert not connections, 'multiple/ambiguous selected bus connections'
            fd = arguments[0]
            slot = task['fds'].get(fd)
            assert slot is not None and not slot.get('lineage'), 'connect fd has no admitted socket lineage'
            conn = {'owner':capture['identity'],'intent':task['key'],'fd':fd,
                    'socket':self.bus_fd(task['key'][0],fd),'tables':[task['fds']],
                    'connected':False,'outcome':None,'acked':False,'name':None}
            slot['bus'] = conn
            connections.append(conn)  # Watching exists BEFORE connect can execute.
            return None
        if not connections: return None
        assert len(connections) == 1, 'one selected socket incarnation'
        conn = connections[0]; table = task['fds']
        assert any(table is known for known in conn['tables']), 'unknown bus FD table incarnation'
        aliases = [fd for fd,value in table.items() if value.get('bus') is conn]
        for fd in aliases: assert self.bus_fd(task['key'][0],fd,conn['connected']) == conn['socket'], 'tracked bus fd reused'
        entries = list((pathlib.Path('/proc')/str(task['key'][0])/'fd').iterdir())
        assert len(entries) <= 4096, 'fresh bus alias FD cap'
        live_aliases = set()
        for entry in entries:
            if os.readlink(entry) == 'socket:['+str(conn['socket'][1])+']':
                assert self.bus_fd(task['key'][0],int(entry.name),conn['connected']) == conn['socket'], 'fresh watched alias identity'
                live_aliases.add(int(entry.name))
        assert live_aliases == set(aliases), 'unconsumed/unknown watched socket alias'
        for other in self.tasks.values():
            if other.get('exited'): continue
            if other['fds'] is table and other['key'] != task['key']:
                pending = self.pending.get(other['key'][0],'')
                assert not pending.startswith(('dup(','dup2(','dup3(','fcntl(','close(','close_range(',
                    'socket(','socketpair(','open(','openat(','openat2(','pipe(','pipe2(','accept(','accept4(')), 'lagging alias/table record'
        fd = arguments[0]
        destroys = (name in ('close','shutdown') and fd in aliases or
                    name in ('dup2','dup3') and arguments[1] in aliases and fd != arguments[1] or
                    name == 'close_range' and any((fd & 0xffffffff) <= alias <= (arguments[1] & 0xffffffff) for alias in aliases) or
                    name == 'exit_group' or name == 'exit' and task['facts']['tgid'] == task['key'][0])
        if name == 'exit' and not destroys:
            owner = self.register(conn['owner']['pid'])
            assert owner['fds'] is table and not owner.get('exited') and owner['key'][1] == conn['owner']['birth'], 'thread exit lacks surviving owner/table'
            assert self.identity(owner['key'][0]) == conn['owner'], 'surviving peer identity drift'
        return conn if destroys and not conn['acked'] else None

    def namespace_child(self, local, parent, flags, outer_hint=None):
        assert 'CLONE_PARENT' not in flags, 'unsupported clone parent relation'
        before = self.register(parent['key'][0])['facts']
        scope = pathlib.Path('/proc')/str(before['tgid'])/'task'
        tids = {int(p.name) for p in scope.iterdir()} if 'CLONE_THREAD' in flags else {int(p.name) for p in pathlib.Path('/proc').iterdir() if p.name.isdecimal()}
        assert len(tids) <= self.MAX_EVENTS, 'clone discovery task budget'
        if outer_hint is not None:
            assert outer_hint in tids, 'translated child outside original task scope'
            tids = {outer_hint}
        matches = []
        discovery_counts = dict.fromkeys(('statusGone','depthMismatch','localMismatch','parentMismatch','pidnsMismatch','matched'),0)
        for tid in sorted(tids):
            ids = []  # Failure context must not carry the preceding candidate's IDs.
            proc = pathlib.Path('/proc')/str(tid)
            try:
                rows = (proc/'status').read_text().splitlines()
            except FileNotFoundError:
                discovery_counts['statusGone'] += 1
                if 'CLONE_THREAD' in flags: raise
                continue  # An unqualified enumeration entry disappeared.
            status = dict(row.split(':',1) for row in rows if ':' in row)
            ids = list(map(int,status['NSpid'].split()))
            if len(ids) == len(before['nspid']) and ids[-1] == local and ('CLONE_THREAD' in flags or int(status['PPid']) == before['tgid']):
                if os.readlink(proc/'ns/pid') != before['ns']['pid']:
                    discovery_counts['pidnsMismatch'] += 1
                    continue
                child = self.register(tid)
                assert child['facts']['nspid'] == ids, 'clone namespace mapping race'
                matches.append(child)
                discovery_counts['matched'] += 1
            else:
                reason = 'depthMismatch' if len(ids) != len(before['nspid']) else 'localMismatch' if ids[-1] != local else 'parentMismatch'
                discovery_counts[reason] += 1
        assert len(matches) == 1, 'one exact namespace child mapping required'
        self.register(parent['key'][0])
        facts = matches[0]['facts']
        if 'CLONE_THREAD' in flags:
            assert facts['tgid'] == before['tgid'], 'clone thread parent group'
        else:
            assert facts['tgid'] == facts['pid'] and facts['ppid'] == before['tgid'], 'clone process parent group'
        if outer_hint is not None:
            assert facts['pid'] == outer_hint, 'translated child mapping mismatch'
        return facts['pid']

    def register(self, tid, parent=None, shared=False):
        facts = trace_task_identity(tid)
        assert facts['ns'] == self.controller['ns'], 'trace namespace drift'
        assert facts['uids'] == facts['gids'] == [1000]*4 and facts['groups'] == [], 'trace credential drift'
        assert facts['tracer'] == self.tracer['pid'], 'untraced task'
        key = (tid, facts['birth'])
        if tid in self.tasks:
            assert self.tasks[tid]['key'] == key, 'trace PID reuse'
            old = self.tasks[tid]['facts']
            assert all(facts[field] == old[field] for field in ('tgid','uids','gids','groups','nspid','tracer','ns','uidMap','gidMap')), 'fresh task authority drift'
            return self.tasks[tid]
        table = parent['fds'] if parent is not None and shared else copy.deepcopy(parent['fds']) if parent else {}
        task = {'key':key,'facts':facts,'fds':table,'parent':parent['key'] if parent else None,
                'capture':parent.get('capture') if parent and shared else None}
        self.tasks[tid] = task
        return task

    def feed(self, chunk):
        with self.lock:
            assert self.error is None, self.error
            self.total += len(chunk)
            assert self.total <= self.MAX_STREAM, 'total raw trace/hex-expansion budget'
            self.fragment += chunk
            while b'\n' in self.fragment:
                line, self.fragment = self.fragment.split(b'\n',1)
                assert len(line) <= self.MAX_LINE, 'trace line budget'
                self.line(line.decode('ascii','strict'))
                self.changed.notify_all()
            assert len(self.fragment) <= self.MAX_LINE, 'partial trace line budget'

    def line(self, line):
        marker = re.fullmatch(r'# R1353 ([1-9][0-9]{0,6})',line)
        if marker:
            sequence = int(marker[1])
            assert sequence <= self.MAX_EVENTS and sequence not in self.checkpoints, 'checkpoint sequence'
            self.checkpoints.add(sequence)
            return
        self.events += 1
        assert self.events <= self.MAX_EVENTS, 'trace record budget'
        m = re.fullmatch(r'(?:\[pid\s+)?(\d+)(?:\])?\s+(.+)',line)
        assert m, 'unqualified trace record'
        tid, call = int(m[1]), m[2]
        remap = re.fullmatch(r'\+\+\+ superseded by execve in pid ([1-9][0-9]*) \+\+\+',call)
        if remap:
            self.exec_remap(tid,int(remap[1]))
            return
        task = self.tasks.get(tid) or self.register(tid)
        if task.get('parent') is None and task['facts']['tgid'] != self.controller['pid'] and not task.get('nativeLeader'):
            clones = [prefix for prefix in self.pending.values() if prefix.startswith(('clone(','clone3(','fork(','vfork('))]
            if clones:
                queued = task.setdefault('earlyChildCalls',[])
                assert len(queued) < 256 and sum(len(value) for value in queued)+len(call) <= LIMIT, 'early child trace bound'
                queued.append(call)
                return
        if call.startswith('--- SIG'):
            assert tid in self.tasks and not self.tasks[tid].get('exited') and (task.get('parent') or task.get('nativeLeader') or task['facts']['tgid'] == self.controller['pid']), 'signal from unknown/terminal task'
            assert 'SIGKILL' not in call, 'forced native termination'
            return
        if call.startswith('+++ killed by '):
            assert call == '+++ killed by SIGTERM +++' and tid in self.tasks, 'unknown/forced terminal signal'
            task = self.tasks[tid]
            assert not task.get('exited') and not task.get('capture') and not task.get('nativeLeader'), 'helper/leader/control termination cannot pass'
            ancestor = task
            while ancestor.get('parent'):
                key = ancestor['parent']; ancestor = self.task_for_key(key)
                assert ancestor['key'] == key, 'terminal ancestry birth mismatch'
                if ancestor.get('nativeLeader'): break
            assert ancestor.get('nativeLeader'), 'non-native terminal signal'
            group = self.tasks.get(task['facts']['tgid'])
            assert group is not None, 'unknown terminal process birth'
            task['terminalCancellation'] = {'record':call,'pendingSyscall':self.pending.pop(tid,None),
                'pendingRead':self.pending_reads.pop(tid,None),'leader':ancestor['key'],'worker':group['key']}
            # Immutable cancel; owned cleanup.
            pending_read = task['terminalCancellation']['pendingRead']
            if pending_read is not None: task['terminalCancellation']['pendingRead'] = {'fd':pending_read[0],'lineageAtEntry':pending_read[1]}
            task['exited'] = True
            return
        if call.startswith('+++ exited with '):
            assert tid in self.tasks and tid not in self.pending and (task.get('parent') or task.get('nativeLeader') or task['facts']['tgid'] == self.controller['pid']), 'unobserved/unfinished exit'
            task = self.tasks[tid]
            capture = task.get('capture')
            if capture and task['facts']['tgid'] == tid:
                assert call == '+++ exited with 0 +++', 'helper natural exit required'
                assert capture['eof'] and capture['bytes'] and capture['dupSeen'], 'incomplete consumed stdin'
                capture['exited'] = True
            task['exited'] = True
            return
        task = self.tasks.get(tid) or self.register(tid)
        assert not task.get('exited'), 'trace after terminal identity'
        if '<unfinished ...>' in call:
            assert tid not in self.pending and call.endswith(' <unfinished ...>'), 'ambiguous unfinished syscall'
            prefix = call[:-len(' <unfinished ...>')]
            if prefix.startswith('read('):
                fd = int(prefix.split('(',1)[1].split(',',1)[0])
                self.pending_reads[tid] = (fd,dict(task['fds'][fd]) if fd in task['fds'] else None,task['fds'])
                if task.get('capture') and task['fds'].get(fd,{}).get('lineage'):
                    capture = task['capture']
                    assert capture['reading'] is None, 'concurrent stdin reads have ambiguous ordering'
                    capture['reading'] = tid
            self.pending[tid] = prefix
            return
        resumed = re.fullmatch(r'<\.\.\. (\w+) resumed>(.*)',call)
        if resumed:
            assert tid in self.pending, 'resumed without unfinished'
            prefix = self.pending.pop(tid)
            assert prefix.startswith(resumed[1]+'('), 'resumed syscall mismatch'
            call = prefix+resumed[2]
            if tid in self.pending_reads:
                fd,old,table = self.pending_reads.pop(tid)
                assert task['fds'] is table and task['fds'].get(fd) == old, 'unfinished read FD lineage changed'
        else:
            assert tid not in self.pending, 'missing resumed record'
        m = re.fullmatch(r'(\w+)\((.*)\)\s+=\s+(.+)',call)
        assert m, 'unknown syscall text'
        name, args, result = m.groups()
        ret = re.match(r'(-?(?:0x[0-9a-f]+|\d+))(?:\s|$)',result)
        restart = re.fullmatch(r'\? ERESTART(?:SYS|NOINTR|NOHAND|_RESTARTBLOCK).*',result)
        assert ret or result == '?' and name in ('exit','exit_group') or restart and name == 'read', 'unknown syscall result'
        number = int(ret[1],0) if ret else None
        capture = task.get('capture')
        if name in ('execve','execveat') and number == 0:
            strings = re.findall(r'"(?:\\x[0-9a-fA-F]{2})*"',args)
            assert strings, 'missing exec argv'
            # Env abbreviated; HOME /proc proof.
            left, sep, rest = args.partition('[')
            argtext, close, tail = rest.partition(']')
            assert sep and close and '...' not in argtext, 'incomplete exec argv'
            argv = [decoded_hex_string(s).decode('utf-8','strict') for s in re.findall(r'"(?:\\x[0-9a-fA-F]{2})*"',argtext)]
            assert len(b'\0'.join(a.encode() for a in argv))+1 <= 8192, 'exec argv budget'
            task['fds'] = {fd:dict(v) for fd,v in task['fds'].items() if not v.get('cloexec')}
            if argv == self.helper_argv:
                ancestor = task
                assert ancestor.get('parent') is not None, 'unobserved helper ancestry'
                while ancestor.get('parent'):
                    parent_key = ancestor['parent']
                    ancestor = self.task_for_key(parent_key)
                    assert ancestor['key'] == parent_key, 'ancestor birth mismatch'
                    if ancestor.get('nativeLeader'): break
                if not ancestor.get('nativeLeader'):
                    assert ancestor['facts']['tgid'] == self.controller['pid'], 'unknown nonnative helper ancestry'
                    task['excludedControllerHelper'] = True
                    return  # Exact observed replay/control descendant; never selected native evidence.
                facts = self.identity(tid)
                assert facts['birth'] == task['key'][1] and facts['argv'] == argv, 'exec live identity/argv mismatch'
                assert facts['exe'] == argv[0] and facts['cwd'] == str(self.root/'home/.cursor'), 'source-qualified USER helper executable/cwd'
                assert facts['ns'] == self.controller['ns'] and facts['uids'] == facts['gids'] == [1000]*4
                assert facts['groups'] == [] and facts['tracer'] == self.tracer['pid']
                assert digest(argv[0]) == self.helper_hash, 'actual helper executable hash'
                env = (pathlib.Path('/proc')/str(tid)/'environ').read_bytes()
                assert len(env) <= 65536 and b'HOME='+str(self.root/'home').encode()+b'\0' in env, 'actual helper HOME'
                endpoint = live_stdin_endpoint(tid)
                capture = {'identity':facts,'endpoint':endpoint,'bytes':bytearray(),'eof':False,
                           'dupSeen':False,'exited':False,'reading':None,
                           'events':[{'tidBirth':task['key'],'syscall':call}],'leader':ancestor['key']}
                assert len(self.selected) < 2, 'one genuine helper per planned phase'
                self.selected.append(capture)
                task['capture'] = capture
                task['fds'][0] = {'lineage':True,'cloexec':False}
            elif capture:
                raise AssertionError('selected helper unexpected exec')
        elif name in ('clone','clone3','fork','vfork') and number is not None and number > 0:
            translated = re.fullmatch(r"([1-9][0-9]{0,9})(?: /\* ([1-9][0-9]{0,9}) in strace's PID NS \*/)?",result)
            assert translated and int(translated[1]) == number and number <= 2147483647 and (translated[2] is None or int(translated[2]) <= 2147483647), 'unknown fork PID namespace return'
            outer_hint = int(translated[2]) if translated[2] is not None else None
            edge = self.clone_edges.pop(task['key'],None)
            assert edge is not None and edge['local'] == number, 'return without held clone admission'
            assert edge['flags'] == set(re.findall(r'\bCLONE_[A-Z0-9_]+\b',args)), 'clone flags changed at return'
            number = edge['key'][0]
            assert outer_hint in (None,number), 'held return outer namespace mismatch'
            child = self.tasks[number]
            assert child['key'] == edge['key'] and child['parent'] == task['key'], 'held return lineage mismatch'
            # vfork child exec/exit may precede return: only reconcile the edge
            # admitted while both tasks were freshly live, never dead admission.
            queued = child.pop('earlyChildCalls',[])
            for recorded in queued: self.line(str(number)+' '+recorded)
        elif name == 'connect':
            conn = task['fds'].get(int(args.split(',',1)[0]),{}).get('bus')
            if conn is not None:
                assert conn['intent'] == task['key'] and conn['outcome'] is None, 'connect outcome incarnation'
                conn['outcome'] = call
                conn['connected'] = number == 0
        elif name in ('dup','dup2','dup3') and number is not None and number >= 0:
            old = int(args.split(',',1)[0])
            if name != 'dup': assert number == int(args.split(',')[1]), 'dup target mismatch'
            if name == 'dup2' and old == number: return  # POSIX no-op preserves FD_CLOEXEC.
            task['fds'][number] = dict(task['fds'].get(old,{'lineage':False}))
            task['fds'][number]['cloexec'] = 'O_CLOEXEC' in args
            if capture and old == 0 and task['fds'][number].get('lineage') and number != 0:
                capture['dupSeen'] = True
        elif name == 'fcntl' and number is not None and number >= 0:
            parts = args.split(','); fd = int(parts[0]); op = parts[1].strip()
            if op in ('F_DUPFD','F_DUPFD_CLOEXEC'):
                task['fds'][number] = dict(task['fds'].get(fd,{'lineage':False}))
                task['fds'][number]['cloexec'] = op.endswith('CLOEXEC')
                if capture and fd == 0 and number != 0 and task['fds'][number].get('lineage'): capture['dupSeen'] = True
            elif op == 'F_SETFD':
                if fd in task['fds']: task['fds'][fd]['cloexec'] = 'FD_CLOEXEC' in args
            else:
                assert op in ('F_GETFD','F_GETFL','F_SETFL','F_SETLK','F_SETLKW','F_GETLK','F_OFD_SETLK'), 'unaccounted fcntl'
        elif name == 'close' and number == 0:
            task['fds'].pop(int(args),None)
        elif name == 'close_range' and number == 0:
            parts = args.split(','); first = int(parts[0]); last = 4294967295 if '~0U' in parts[1] else int(parts[1])
            if 'CLOSE_RANGE_UNSHARE' in args:
                task['fds'] = {fd:dict(value) for fd,value in task['fds'].items()}
                for conn in (capture or {}).get('busConnections',[]):
                    assert len(conn['tables']) < 4096, 'bus table lineage cap'
                    conn['tables'].append(task['fds'])
            for fd in list(task['fds']):
                if first <= fd <= last:
                    if 'CLOSE_RANGE_CLOEXEC' in args: task['fds'][fd]['cloexec'] = True
                    else: task['fds'].pop(fd)
        elif name in ('open','openat','openat2','socket','accept','accept4','eventfd','eventfd2','epoll_create','epoll_create1','memfd_create') and number is not None and number >= 0:
            task['fds'][number] = {'lineage':False,'cloexec':'CLOEXEC' in args}
        elif name in ('pipe','pipe2','socketpair') and number == 0:
            fds = re.findall(r'\[(\d+),\s*(\d+)\]',args)
            assert len(fds) == 1, 'unknown descriptor pair'
            for fd in map(int,fds[0]): task['fds'][fd] = {'lineage':False,'cloexec':'CLOEXEC' in args}
        elif name == 'read':
            assert not any(any(value.startswith('read(') for value in other.get('earlyChildCalls',[])) for other in self.tasks.values()), 'ambiguous consumed order across unresolved child clone'
            fd = int(args.split(',',1)[0])
            if capture and task['fds'].get(fd,{}).get('lineage'):
                assert capture['dupSeen'] and fd != 0 and not capture['eof'], 'read outside consumed alias/completion'
                assert capture['reading'] in (None,tid), 'ambiguous read order'
                capture['reading'] = None
                if restart: pass
                elif number == -1:
                    assert any(result.startswith('-1 '+e) for e in ('EINTR','EAGAIN')), 'failed stdin read'
                else:
                    m = re.fullmatch(r'\d+,\s*("(?:\\x[0-9a-fA-F]{2})*"),\s*(\d+)',args)
                    assert m and number is not None and 0 <= number <= int(m[2]), 'partial/unknown stdin read'
                    raw = decoded_hex_string(m[1])
                    assert len(raw) == number, 'decoded return count mismatch/truncation'
                    capture['bytes'].extend(raw)
                    assert len(capture['bytes']) <= LIMIT, 'consumed stdin payload budget'
                    capture['eof'] = number == 0
                assert len(capture['events']) < 4096, 'selected read/lineage event budget'
                capture['events'].append({'tidBirth':task['key'],'syscall':call})
        elif capture and name in ('readv','pread64','preadv','preadv2','recvfrom','recvmsg','recvmmsg'):
            fd = int(args.split(',',1)[0])
            assert not task['fds'].get(fd,{}).get('lineage'), 'unknown read on proven stdin lineage: '+name
            if name in ('recvmsg','recvmmsg') and number is not None and number >= 0:
                assert 'SCM_RIGHTS' not in args and ('msg_controllen=0' in args or 'msg_control=NULL' in args or 'msg_control=[]' in args), 'unknown received descriptor lineage'
        elif capture and name in ('splice','tee','vmsplice','io_uring_setup','io_uring_enter','io_uring_register','pidfd_getfd','unshare','setns','setuid','setgid','setreuid','setregid','setresuid','setresgid','setgroups','capset'):
            raise AssertionError('unaccounted selected read/FD/identity operation: '+name)
        if capture and name in ('dup','dup2','dup3','fcntl','close','close_range','clone','clone3','fork','vfork','exit','exit_group'):
            assert len(capture['events']) < 4096, 'selected lineage event budget'
            capture['events'].append({'tidBirth':task['key'],'syscall':call})

    def finish(self):
        with self.lock:
            assert not self.fragment and not self.pending and not self.pending_reads and self.error is None and not self.clone_edges and not self.checkpoints and not any(task.get('earlyChildCalls') for task in self.tasks.values()), 'partial trace stream/unknown child ancestry'
            assert all(c['exited'] and c['eof'] and c['reading'] is None for c in self.selected), 'unfinished helper stream'
            return self.selected


def task_key(task):
    return (task['facts']['pid'],task['facts']['birth'])


TRACE_SYSCALLS = (
    'read,readv,pread64,preadv,preadv2,recvfrom,recvmsg,recvmmsg,splice,tee,vmsplice,'
    'dup,dup2,dup3,fcntl,close,close_range,open,openat,openat2,pipe,pipe2,socket,socketpair,'
    'accept,accept4,eventfd,eventfd2,epoll_create,epoll_create1,memfd_create,pidfd_getfd,connect,shutdown,'
    'execve,execveat,clone,clone3,fork,vfork,exit,exit_group,wait4,waitid,'
    'setns,unshare,setuid,setgid,setreuid,setregid,setresuid,setresgid,setgroups,capset,'
    'io_uring_setup,io_uring_enter,io_uring_register')


def control_send(peer, value, clock):
    raw = json.dumps(value,separators=(',',':')).encode()
    assert len(raw) <= 2*LIMIT, 'control record bound including base64 expansion'
    peer.settimeout(clock.wait(5))
    peer.sendall(struct.pack('>I',len(raw))+raw)


def control_receive(peer, clock):
    def exact(n):
        parts = bytearray()
        while len(parts) < n:
            peer.settimeout(clock.wait(5))
            part = peer.recv(n-len(parts))
            assert part, 'owned control peer EOF'
            parts.extend(part)
        return bytes(parts)
    size = struct.unpack('>I',exact(4))[0]
    assert 0 < size <= 2*LIMIT, 'control record size including base64 expansion'
    return strict_json(exact(size))


class ControllerObservation:
    def __init__(self, peer, clock):
        self.peer, self.clock = peer, clock
        self.sequence = 0
        self.nonce = os.urandom(32).hex()
        self.exchange_lock = threading.Lock()

    def exchange(self, action, **facts):
        if not self.exchange_lock.acquire(timeout=self.clock.wait(1)): raise AssertionError('control exchange lock deadline')
        try: return self._exchange_locked(action,**facts)
        finally: self.exchange_lock.release()

    def _exchange_locked(self, action, **facts):
        self.sequence += 1
        control_send(self.peer,{'action':action,'sequence':self.sequence,'nonce':self.nonce,'cleanupUsed':self.clock.cleanup_used,**facts},self.clock)
        reply = control_receive(self.peer,self.clock)
        assert reply.get('sequence') == self.sequence and reply.get('nonce') == self.nonce, 'control synchronization identity'
        assert reply.get('ok') is True, reply.get('error','unknown outer observation')
        return reply


def source_failure_frames(error, trusted_code):
    frames, tb, visited = [], error.__traceback__, 0
    while tb is not None and visited < 64:
        code = tb.tb_frame.f_code
        if code.co_filename == trusted_code.co_filename:
            frames = (frames+[(code.co_name[:64],tb.tb_lineno)])[-8:]
        tb = tb.tb_next; visited += 1
    return frames


class PassiveStrace:
    def __init__(self, controller, installed, root, clock, tools, session):
        self.session = session
        self.incarnation = os.urandom(32).hex()
        self.observer_sequence = 0
        self.acked_keys = set()
        self.pending_acks = {}
        self.custody_keys = set()
        self.held_workers = []
        self.stopping = False
        self.observer_peer, inherited = socket.socketpair(socket.AF_UNIX,socket.SOCK_SEQPACKET)
        self.clock = clock
        self.controller = controller
        self.root = pathlib.Path(root)
        self.error = None
        self.first_error = None
        self.options = None
        self.seize_stops = set()
        self.debug_bytes = 0
        self.debug_fragment = b''
        self.started_tids = set()
        self.closing_tids = set()
        readfd, writefd = os.pipe()
        primary, selector, binding, generation = installed
        argv = [tools['strace']['path'],'-d','-f','-xx','--decode-pids=pidns','-s',str(LIMIT+1),
                '-e','trace='+TRACE_SYSCALLS,'-o','/proc/self/fd/'+str(writefd),'-p',str(controller['pid'])]
        self.child = subprocess.Popen(argv,stdin=subprocess.DEVNULL,stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,pass_fds=(writefd,inherited.fileno()),env={'PATH':'/usr/bin:/bin','LANG':'C',
                'TMPDIR':str(self.root/'tmp'),'TEST_R1353_FD':str(inherited.fileno()),
                'TEST_R1353_INCARNATION':self.incarnation},start_new_session=True,bufsize=0)
        inherited.close()
        os.close(writefd)
        self.tracer = startup_identity(self.child.pid)
        self.tracer = proc_identity(self.child.pid)
        assert self.tracer['argv'] == argv and self.tracer['exe'] == tools['strace']['path']
        assert self.tracer['uids'] == self.tracer['gids'] == [0]*4
        assert self.tracer['ns']['pid'] != controller['ns']['pid'] and self.tracer['ns']['user'] == controller['ns']['user']
        assert self.tracer['uidMap'] == controller['uidMap'] and self.tracer['gidMap'] == controller['gidMap']
        assert self.tracer['ns']['pid'] == os.readlink('/proc/self/ns/pid') == os.readlink('/proc/1/ns/pid'), 'tracer/parser proc PID namespace mismatch'
        self.parser = StdinAliasTrace(proc_identity,[str(primary),'cursor-event','stop','--binding',str(selector)],
                                     digest(primary),controller,self.tracer,root)
        self.trace_pipe = os.fdopen(readfd,'rb',buffering=0)
        self.threads = []
        def pump(stream, consume):
            try:
                while True:
                    part = stream.read(8192)
                    if not part: break
                    with self.parser.changed:
                        try: consume(part)
                        except Exception as error:
                            self.refuse_held(error)
                            raise
            except Exception as error:
                self.refuse_held(error)
                tb = error.__traceback__
                try:
                    print('trace-pump-failure',type(error).__name__[:64],'firstStored',error is self.first_error,source_failure_frames(error,pump.__code__),file=sys.stderr,flush=True)
                    call_name = None
                    while tb is not None:
                        if tb.tb_frame.f_code is self.parser.line.__func__.__code__:
                            name = tb.tb_frame.f_locals.get('name')
                            if name in ('clone','clone3','fork','vfork'): call_name = name
                        if tb.tb_frame.f_code is self.parser.namespace_child.__func__.__code__:
                            context = tb.tb_frame.f_locals
                            parent = context['parent']; before = context.get('before',parent['facts'])
                            reported_flags = tuple(flag for flag in ('CLONE_THREAD','CLONE_FILES','CLONE_VM','CLONE_VFORK',
                                'CLONE_PARENT','CLONE_PARENT_SETTID','CLONE_CHILD_SETTID','CLONE_CHILD_CLEARTID',
                                'CLONE_NEWPID','CLONE_NEWUSER','CLONE_NEWNS','CLONE_NEWNET') if flag in context['flags'])
                            hint_snapshot = None
                            hint = context.get('outer_hint')
                            if error is self.first_error and type(error) is AssertionError and str(error) == 'translated child outside original task scope' and type(hint) is int and hint > 0 and hint not in context.get('tids',()):
                                try:
                                    snapshot = []
                                    for field in ('stat','status'):
                                        with (pathlib.Path('/proc')/str(hint)/field).open('rb') as hint_stream: raw = hint_stream.read(8193)
                                        if len(raw) > 8192: raise ValueError('diagnostic byte budget')
                                        snapshot.append(raw.decode('ascii','strict'))
                                    raw, status_raw = snapshot; tail = raw[raw.rindex(')')+2:].split()
                                    number = lambda value: int(value) if re.fullmatch(r'[0-9]{1,20}',value) and int(value) <= 18446744073709551615 else (_ for _ in ()).throw(ValueError('diagnostic integer'))
                                    pid, birth = number(raw[:raw.index('(')].strip()), number(tail[19])
                                    if pid != hint or tail[0] not in ('R','S','D','Z','T','t','X','x','K','W','P','I'): raise ValueError('diagnostic stat')
                                    status = {}
                                    for line in status_raw.splitlines():
                                        key, sep, value = line.partition(':')
                                        if sep and key in ('Tgid','NSpid','Uid','TracerPid'):
                                            if key in status: raise ValueError('diagnostic duplicate')
                                            status[key] = value.split()
                                    if set(status) != {'Tgid','NSpid','Uid','TracerPid'} or len(status['Tgid']) != 1 or len(status['TracerPid']) != 1 or len(status['Uid']) != 4 or not 1 <= len(status['NSpid']) <= 32: raise ValueError('diagnostic status')
                                    hint_snapshot = {'pid':pid,'birth':birth,'state':tail[0],'tgid':number(status['Tgid'][0]),'nspid':[number(v) for v in status['NSpid']],'uids':[number(v) for v in status['Uid']],'tracer':number(status['TracerPid'][0])}
                                except Exception as diagnostic_error:
                                    hint_snapshot = {'error':type(diagnostic_error).__name__[:64],'errno':diagnostic_error.errno if isinstance(diagnostic_error,OSError) and type(diagnostic_error.errno) is int else None}
                            print('namespace-registration-failure',
                                'syscall',call_name,'cloneFlags',reported_flags,'otherFlagCount',len(context['flags'])-len(reported_flags),
                                'refusals',context.get('discovery_counts'),
                                'registrationTid',tb.tb_next.tb_frame.f_locals.get('tid') if tb.tb_next is not None else None,
                                'outerHint',context.get('outer_hint') if type(context.get('outer_hint')) is int else None,
                                'outerHintSnapshot',hint_snapshot,
                                'candidate',(context['local'],context.get('tid'),context.get('ids',[])[:32]),
                                'discovery',(len(context.get('tids',())),sorted(context.get('tids',()))[:16],len(context.get('matches',())),[task['key'] for task in context.get('matches',())[:16]]),
                                'parent',(parent['key'],before['tgid'],before['nspid'][:32]),
                                'CLONE_THREAD','CLONE_THREAD' in context['flags'],
                                'roles',(bool(parent.get('nativeLeader')),bool(parent.get('capture'))),file=sys.stderr,flush=True)
                        tb = tb.tb_next
                except Exception:
                    pass
                if error is self.first_error and type(error) is AssertionError and str(error) == 'trace namespace drift':
                    try:
                        tb, visited = error.__traceback__, 0
                        while tb is not None and visited < 64:
                            if tb.tb_frame.f_code is self.parser.register.__func__.__code__:
                                context = tb.tb_frame.f_locals
                                facts, expected = context['facts'], context['self'].controller['ns']
                                pid, birth = context['tid'], facts['birth']
                                if type(pid) is not int or not 0 < pid <= 2147483647 or type(birth) is not int or not 0 <= birth <= 18446744073709551615:
                                    raise ValueError('diagnostic identity budget')
                                namespaces = {}
                                for side, values in (('actual',facts['ns']),('expected',expected)):
                                    namespaces[side] = {}
                                    for kind in ('pid','net','user'):
                                        value = values[kind]
                                        if type(value) is not str or len(value) > 64: raise ValueError('diagnostic namespace budget')
                                        match = re.fullmatch(kind+r':\[(0|[1-9][0-9]{0,19})\]',value)
                                        if match is None or int(match[1]) > 18446744073709551615: raise ValueError('diagnostic namespace identity')
                                        namespaces[side][kind] = value
                                print('trace-register-namespace-refusal',{'pid':pid,'birth':birth,**namespaces},file=sys.stderr,flush=True)
                                break
                            tb = tb.tb_next; visited += 1
                    except Exception:
                        pass
            finally:
                stream.close()
                if not self.stopping and self.error is None:
                    self.refuse_held(AssertionError('trace/debug reader EOF before owned close'))
        for stream, consume in ((self.trace_pipe,self.consume_trace),(self.child.stderr,self.consume_debug)):
            t = threading.Thread(target=pump,args=(stream,consume),daemon=True)
            self.threads.append(t); t.start()
        dispatcher = threading.Thread(target=self.dispatch_held,daemon=True)
        self.threads.append(dispatcher); dispatcher.start()
        self.ready()
        (self.root/'evidence/trace-readiness.json').write_text(json.dumps({
            'controller':controller,'tracer':self.tracer,'currentTids':sorted(self.started_tids),
            'seizeStopTids':sorted(self.seize_stops),'ptraceOptions':self.options,'argv':argv,
            'toolProvenance':tools['strace'],'incarnation':self.incarnation,
            'kernelACKCompletedKeys':sorted(self.acked_keys)},indent=2)+'\n')

    def refuse_held(self, error):
        with self.parser.changed:
            if self.error is None:
                self.first_error = error
                self.error = self.session.error or type(error).__name__+':'+str(error)[:240]
                self.parser.error = self.error
            self.parser.changed.notify_all()
        with self.session.records_changed:
            self.session.trace_failure = self.error
            self.session.records_changed.notify_all()
        try: self.observer_peer.shutdown(socket.SHUT_RDWR)
        except OSError: pass

    def dispatch_held(self):
        try:
            while not self.stopping:
                assert self.error is None and self.session.error is None, self.error or self.session.error
                if not select.select([self.observer_peer],[],[],self.clock.wait(.25))[0]: continue
                packet, ancillary, flags, _ = self.observer_peer.recvmsg(516)
                if self.stopping: break
                assert packet and not ancillary and not flags, 'held channel EOF/truncation/ancillary'
                if packet.startswith(b'DONE '):
                    request = packet[5:]
                    with self.parser.changed:
                        keys = self.pending_acks.pop(request,None)
                        assert keys is not None, 'unrequested/duplicate kernel restart receipt'
                        self.acked_keys.update(keys)
                        self.parser.changed.notify_all()
                    continue
                self.ack_held(packet)
        except Exception as error:
            if not self.stopping: self.refuse_held(error)

    def ack_held(self, packet):
        """Closed checkpoint dispatcher. No lock spans monitor/channel waiting."""
        try:
            assert type(packet) is bytes and 0 < len(packet) < 512, 'held request packet cap/EOF'
            fields = packet.decode('ascii','strict').split()
            assert len(fields) == 17 and fields[:3] == ['R1353',self.incarnation,str(self.tracer['pid'])], 'held tracer incarnation'
            assert all(re.fullmatch(r'0|[1-9][0-9]{0,19}',fields[i]) for i in (3,5,6,7,8,10,11,12,13,14,15,16)), 'held numeric fields'
            seq,phase,tid,birth,child,child_birth = int(fields[3]),fields[4],*map(int,fields[5:9])
            assert seq == self.observer_sequence+1 and seq <= self.parser.MAX_EVENTS, 'held sequence'
            assert phase in ('task','clone','entry','record') and re.fullmatch(r'[a-z][a-z0-9_]{0,31}',fields[9]), 'closed held phase/syscall'
            assert 0 < tid <= 2147483647 and 0 < birth < 1<<64 and 0 <= child <= 2147483647 and 0 <= child_birth < 1<<64, 'held task/birth cap'
            assert (child > 0 and child_birth > 0) if phase == 'clone' else (child == child_birth == 0), 'held pair fields'
            arguments = list(map(int,fields[11:])); assert all(v < 1<<64 for v in arguments), 'held ABI argument cap'
            assert int(fields[10]) < 1<<64, 'held clone flags cap'
            self.observer_sequence = seq
            with self.parser.changed:
                assert self.parser.changed.wait_for(lambda:self.error or seq in self.parser.checkpoints,self.clock.wait(1)), 'complete real trace checkpoint deadline'
                assert self.error is None and self.session.error is None, self.error or self.session.error
                self.parser.checkpoints.remove(seq)
                task = self.parser.register(tid)
                assert task['key'] == (tid,birth), 'held task birth'
                raw = (pathlib.Path('/proc')/str(tid)/'stat').read_text()
                assert raw[raw.rindex(')')+2:].split()[0] == 't', 'actual held kernel stop'
                self.custody_keys.add(task['key'])
                if phase == 'clone':
                    fresh_child = trace_task_identity(child)
                    assert fresh_child['birth'] == child_birth and fresh_child['tracer'] == self.tracer['pid'], 'owned held child birth/tracer'
                    for field in ('uids','gids','groups','ns','uidMap','gidMap'):
                        assert fresh_child[field] == self.controller[field], 'owned held child authority'
                    assert fresh_child['tgid'] == task['facts']['tgid'] or fresh_child['ppid'] == task['facts']['tgid'], 'owned held child parent/group'
                    self.custody_keys.add((child,child_birth))
                table = task['fds']
                if phase == 'clone': self.parser.held_clone(tid,child,birth,child_birth,int(fields[10]))
                conn = self.parser.held_entry(task,fields[9],arguments,self.session) if phase == 'entry' else None
                capture = task.get('capture')
            context = (packet,seq,phase,tid,birth,fields[9],arguments,table,capture,conn)
            if conn is not None:
                with self.parser.changed:
                    assert len(self.held_workers) < 4096, 'held credential worker cap'
                    worker = threading.Thread(target=self.complete_held,args=(context,),daemon=True)
                    self.held_workers.append(worker); worker.start()
            else: self.complete_held(context)
        except Exception as error:
            self.refuse_held(error)
            raise

    def complete_held(self, context):
        packet,seq,phase,tid,birth,syscall,arguments,table,capture,conn = context
        try:
            name = self.session.held_credentials(capture,conn) if conn is not None else None
            # Revalidate the real stopped task, table and every alias AFTER the
            # monitor wait. The callback's cached credential alone cannot ACK.
            with self.parser.changed:
                try:
                    assert self.error is None and self.session.error is None, self.error or self.session.error
                    task = self.parser.register(tid)
                    assert task['key'] == (tid,birth) and task['fds'] is table, 'held task/table incarnation changed'
                    raw = (pathlib.Path('/proc')/str(tid)/'stat').read_text()
                    assert raw[raw.rindex(')')+2:].split()[0] == 't', 'held stop lost before ACK'
                    if capture: assert proc_identity(capture['identity']['pid'],capture['identity']) == capture['identity'], 'fresh selected ACK identity'
                    if conn is not None:
                        assert self.parser.held_entry(task,syscall,arguments,self.session) is conn, 'held socket/table changed after credential wait'
                        assert conn['name'] == name and conn['connected'], 'held name/connection incarnation changed'
                    with self.session.records_lock:
                        if conn is not None:
                            assert name in self.session.active_names and self.session.credentials.get(name) == capture['identity'], 'active monitored name incarnation'
                            assert all(self.session.roundtrip_present(call) for call in self.session.credential_receipts[name]), 'consumed original credential roundtrips lost'
                        assert self.clock.wait(1) > 0 and self.error is None and self.session.error is None, 'held ACK deadline/first error'
                        keys = [task['key']]
                        if phase == 'clone':
                            child_tid,child_birth = map(int,packet.decode('ascii').split()[7:9])
                            child = self.parser.register(child_tid)
                            assert child['key'] == (child_tid,child_birth), 'child birth lost before ACK'
                            child_stat = (pathlib.Path('/proc')/str(child_tid)/'stat').read_text()
                            assert child_stat[child_stat.rindex(')')+2:].split()[0] == 't', 'child stop lost before ACK'
                            edge = self.parser.clone_edges[task['key']]
                            assert self.parser.clone_fds(tid,child_tid,edge['flags']) == edge['fds'], 'fresh inherited FD proof changed before ACK'
                            assert trace_task_identity(tid) == task['facts'] and trace_task_identity(child_tid) == child['facts'], 'fresh clone authority bookends before ACK'
                            keys.append(child['key'])
                        assert packet not in self.pending_acks, 'duplicate ACK authority'
                        self.pending_acks[packet] = keys
                        assert self.observer_peer.send(b'ACK '+packet,socket.MSG_DONTWAIT) == len(packet)+4, 'exact held ACK write'
                        if conn is not None:
                            conn['acked'] = True  # Publish only after the exact ACK is accepted by the channel.
                            capture.setdefault('busLifetimeReceipts',[]).append({'sequence':seq,'tidBirth':task['key'],
                                'fd':conn['fd'],'socket':conn['socket'],'name':name,
                                'controls':self.session.credential_receipts[name]})
                        self.parser.changed.notify_all()
                except Exception as error:
                    self.refuse_held(error)  # Store failure before releasing validation/send locks.
                    raise
        except Exception as error:
            self.refuse_held(error)
            raise

    def consume_trace(self, raw):
        # Require syscall readiness.
        self.parser.feed(raw)

    def consume_debug(self, raw):
        self.debug_bytes += len(raw)
        assert self.debug_bytes <= 256*LIMIT, 'bounded strace diagnostic stream'
        self.debug_fragment += raw
        while b'\n' in self.debug_fragment:
            rawline,self.debug_fragment = self.debug_fragment.split(b'\n',1)
            assert len(rawline) <= 8192, 'strace diagnostic line bound'
            line = rawline.decode('ascii','strict')
            if 'ptrace_setoptions = ' in line:
                self.options = int(line.rsplit(' = ',1)[1],16)
                assert self.options == 0x5f, 'source follow-fork option set'
            stop = re.search(r'\[wait\(0x[0-9a-f]+\) = (\d+)\].*EVENT_STOP \(128\)',line)
            if stop: self.seize_stops.add(int(stop[1]))
            startup = re.search(r'pid (\d+) has TCB_STARTUP, initializing it',line)
            if startup: self.started_tids.add(int(startup[1]))
            if 'detached' in line:
                detach = re.search(r'Process (\d+) detached',line)
                assert detach and int(detach[1]) in self.closing_tids, 'trace detached before native join'
            assert not any(s in line for s in ('PTRACE_SEIZE doesn\'t work','setting opts ','Operation not permitted',
                'Permission denied','attach: ptrace(','unknown pid','No such process')), 'strace attach/fallback uncertainty'
        assert len(self.debug_fragment) <= 8192, 'partial strace diagnostic budget'

    def ready(self):
        end = time.monotonic()+self.clock.wait(5)
        while time.monotonic() < end:
            assert self.error is None and self.child.poll() is None, self.error or 'tracer exited before readiness'
            current = proc_identity(self.controller['pid'],self.controller)
            assert current['ns'] == self.controller['ns'] and current['uids'] == current['gids'] == [1000]*4 and current['groups'] == []
            tids = {int(p.name) for p in (pathlib.Path('/proc')/str(current['pid'])/'task').iterdir()}
            traced = []
            for tid in tids:
                facts = proc_identity(tid)
                if facts['tracer'] == self.tracer['pid']: traced.append(tid)
            if self.options == 0x5f and tids <= self.seize_stops and tids <= self.started_tids and set(traced) == tids:
                with self.parser.changed:
                    assert self.parser.changed.wait_for(lambda:self.error or all(
                        (tid,trace_task_identity(tid)['birth']) in self.acked_keys for tid in tids),
                        max(.001,end-time.monotonic())), 'derived held TEST ACK readiness deadline'
                    assert self.error is None, self.error
                with self.parser.lock:
                    for tid in tids: self.parser.register(tid)
                return
            time.sleep(min(.01,max(0,end-time.monotonic())))
        raise IncompleteInstalledContract('no complete source-qualified SEIZE/TID readiness; native not launched')

    def select_leader(self, local, end):
        assert self.error is None and self.child.poll() is None
        matches = []
        for task in list(self.parser.tasks.values()):
            f = task['facts']
            if f['birth'] == local['birth'] and f['nspid'][-1] == local['pid'] and f['tgid'] == f['pid']:
                matches.append(task)
        assert len(matches) == 1, 'exact trace/native leader mapping missing'
        task = matches[0]
        for attempt in range(1000):
            assert self.error is None and self.child.poll() is None
            assert time.monotonic() < end, 'trace/native leader synchronization deadline'
            try:
                actual = proc_identity(task['facts']['pid'],task['facts'],leader_parent=self.controller['pid'])
                break
            except ProcessObservationRace:
                if attempt == 999: raise
                time.sleep(min(.001,max(0,end-time.monotonic())))
        assert time.monotonic() < end, 'trace/native leader synchronization deadline'
        assert actual['ppid'] == self.controller['pid'] and actual['ns'] == local['ns']
        assert actual['pgid'] == actual['sid'] == actual['pid']
        task['nativeLeader'] = True
        return actual

    def begin_phase(self):
        assert self.error is None and self.child.poll() is None
        tids = {int(p.name) for p in (pathlib.Path('/proc')/str(self.controller['pid'])/'task').iterdir()}
        with self.parser.lock:
            assert set(self.parser.pending) <= tids, 'previous native exit stream incomplete'
            for tid, task in list(self.parser.tasks.items()):
                if tid not in tids:
                    assert task.get('exited'), 'previous native/control trace task not terminal'
                    del self.parser.tasks[tid]

    def phase(self, local_leader, cleanup_signal, cleanup_roles):
        assert self.error is None and self.child.poll() is None, self.error
        with self.parser.lock:
            captures = [c for c in self.parser.selected if c['leader'][1] == local_leader['birth']]
            assert len(captures) == 1 and captures[0]['exited'] and captures[0]['eof'], 'actual native helper capture absent/incomplete'
            capture = captures[0]
            terminal = [task for task in self.parser.tasks.values() if task.get('terminalCancellation',{}).get('leader') == capture['leader']]
            approved = {(row['pid'],row['startTicks']) for row in cleanup_roles if row.get('workerServerArgvMatches') is True and row.get('executableIsPinnedNode') is True and row.get('cwdMatches') is True and row.get('uid') == 1000}
            for task in terminal:
                worker = self.parser.tasks[task['terminalCancellation']['worker'][0]]
                assert worker['key'] == task['terminalCancellation']['worker'], 'terminal worker PID reuse'
                assert cleanup_signal is True and (worker['facts']['nspid'][-1],worker['key'][1]) in approved, 'SIGTERM lacks exact original approved worker cleanup'
            capture['ownedTerminalCancellations'] = [task['terminalCancellation'] for task in terminal]
            assert capture['reading'] is None
            raw = bytes(capture['bytes']); facts = captured_stop_bytes(raw)
            return capture,raw,facts

    def force_contain(self, error):
        """Fatal containment precedes killing a tracer with held exit stops."""
        if hasattr(self,'parser'): self.refuse_held(error)
        self.stopping = True
        try:
            raw = (pathlib.Path('/proc')/str(self.controller['pid'])/'stat').read_text()
        except FileNotFoundError: raw = None
        if raw is not None:
            tail = raw[raw.rindex(')')+2:].split()
            assert int(tail[19]) == self.controller['birth'], 'controller containment refuses PID reuse'
            if tail[0] not in ('Z','X','x'):
                actual = trace_task_identity(self.controller['pid'])
                for field in ('pid','birth','ppid','tgid','uids','gids','groups','nspid','ns','uidMap','gidMap'):
                    assert actual[field] == self.controller[field], 'owned controller containment authority'
                assert actual['tracer'] in (0,self.tracer['pid']), 'foreign controller tracer at containment'
                os.kill(actual['pid'],signal.SIGKILL)  # PID1 death contains its entire owned namespace.
        if self.child.poll() is None:
            raw = (pathlib.Path('/proc')/str(self.child.pid)/'stat').read_text()
            tail = raw[raw.rindex(')')+2:].split()
            assert int(tail[19]) == self.tracer['birth'] and int(tail[1]) == os.getpid(), 'owned tracer containment birth/parent'
            assert int(tail[2]) == int(tail[3]) == self.child.pid, 'owned tracer containment session'
            if tail[0] not in ('Z','X','x'): os.kill(self.child.pid,signal.SIGKILL)

    def failure_join(self, end):
        """Called after the owned namespace supervisor has been killed/joined.

        Containment is failure evidence. Neither channel EOF nor ESRCH is a
        successful ACK or natural join; a foreign/reused birth refuses cleanup.
        """
        error = self.first_error or AssertionError('owned forced namespace containment')
        if hasattr(self,'parser'): self.refuse_held(error)
        self.stopping = True
        keys = set(getattr(self,'custody_keys',()))
        if hasattr(self,'parser'):
            with self.parser.changed:
                keys.update(t['key'] for t in self.parser.tasks.values())
                self.parser.changed.notify_all()
        for tid,birth in keys:
            path = pathlib.Path('/proc')/str(tid)
            if path.exists():
                raw = (path/'stat').read_text(); tail = raw[raw.rindex(')')+2:].split()
                assert int(tail[19]) == birth, 'failure join refuses reused PID'
                raise AssertionError('owned namespace task survives supervisor join')
        self.child.wait(timeout=max(.001,end-time.monotonic()))
        self.observer_peer.close()
        for worker in getattr(self,'threads',[])+getattr(self,'held_workers',[]):
            worker.join(timeout=max(0,end-time.monotonic()))
            assert not worker.is_alive(), 'owned failed observer worker join deadline'
        (self.root/'evidence/trace-failure-join.json').write_text(json.dumps({
            'firstFailure':self.error,'forcedContainment':True,'naturalCompletion':False,
            'ownedBirthsAbsent':sorted(keys),'tracerExit':self.child.returncode,
            'allObserverWorkersJoined':True},indent=2)+'\n')

    def close(self):
        began = time.monotonic(); end = self.clock.cleanup_deadline()
        # Native-empty P1: C TIDs.
        cpid = self.controller['pid']
        for p in pathlib.Path('/proc').iterdir():
            if not p.name.isdecimal(): continue
            try: same = os.readlink(p/'ns/pid') == self.controller['ns']['pid']
            except FileNotFoundError: continue
            if same: assert int(p.name) == cpid, 'native descendants survive before trace join'
        proc_identity(self.child.pid,self.tracer)
        with self.parser.changed:
            assert not self.pending_acks and all(not t.is_alive() for t in self.held_workers), 'held ACK/credential joins incomplete'
            self.stopping = True
        self.closing_tids = {int(p.name) for p in (pathlib.Path('/proc')/str(cpid)/'task').iterdir()}
        os.kill(self.child.pid,signal.SIGINT)
        self.child.wait(timeout=min(self.clock.wait(2,cleanup=True),max(.001,end-time.monotonic())))
        self.observer_peer.close()
        for t in self.threads+self.held_workers: t.join(timeout=max(0,end-time.monotonic()))
        assert all(not t.is_alive() for t in self.threads+self.held_workers) and self.error is None, 'trace pump/join failure'
        with self.parser.lock:
            assert set(self.parser.pending) <= self.closing_tids, 'unjoined native unfinished syscall'
            assert set(self.parser.pending_reads) <= self.closing_tids, 'unjoined native pending read'
            self.parser.pending_reads.clear()
            self.parser.pending.clear()  # C's blocked control read ends on this intentional detach.
        self.parser.finish()
        self.clock.charge_cleanup(began)


def dbus_types(signature):
    assert type(signature) is str and len(signature) <= 255, 'DBus signature budget'
    def parse(at, depth, dictionary=False):
        assert depth <= 16 and at < len(signature), 'DBus signature truncation/depth'
        ch = signature[at]
        if ch in 'ybnqiuxtdhsogv': return ch, at+1
        if ch == 'a':
            item, end = parse(at+1,depth+1,True)
            return ('a',item), end
        assert ch == '(' or ch == '{' and dictionary, 'unsupported DBus signature'
        closing = ')' if ch == '(' else '}'
        values = []; at += 1
        while at < len(signature) and signature[at] != closing:
            value, at = parse(at,depth+1)
            values.append(value)
            assert len(values) <= 32, 'DBus struct signature budget'
        assert at < len(signature) and values, 'unclosed/empty DBus struct'
        if ch == '{':
            assert len(values) == 2 and type(values[0]) is str and values[0] in 'ybnqiuxtdhsog', 'DBus dictionary key'
        return (ch,tuple(values)), at+1
    values = []; at = 0
    while at < len(signature):
        value, at = parse(at,0)
        values.append(value)
    return values


class DBusValueReader:
    def __init__(self, raw, endian, at=0, end=None):
        self.raw, self.endian, self.at = raw, endian, at
        self.end = len(raw) if end is None else end
        self.entries = 0

    def align(self, n):
        end = (self.at+n-1)//n*n
        assert end <= self.end and not any(self.raw[self.at:end]), 'DBus alignment/padding'
        self.at = end

    def take(self, n):
        assert 0 <= n <= self.end-self.at, 'partial DBus value'
        value = self.raw[self.at:self.at+n]; self.at += n
        return value

    def number(self, fmt, align):
        self.align(align)
        return struct.unpack(self.endian+fmt,self.take(struct.calcsize(fmt)))[0]

    def value(self, typ, depth=0):
        self.entries += 1
        assert self.entries <= 4096 and depth <= 16, 'DBus entry/depth bounds'
        if type(typ) is str:
            if typ in 'ybnqiuxtdh':
                fmt, alignment = {'y':('B',1),'b':('I',4),'n':('h',2),'q':('H',2),
                    'i':('i',4),'u':('I',4),'x':('q',8),'t':('Q',8),'d':('d',8),'h':('I',4)}[typ]
                value = self.number(fmt,alignment)
                if typ == 'b':
                    assert value in (0,1), 'invalid DBus boolean'
                    return bool(value)
                return value
            if typ in 'sog':
                size = self.number('B',1) if typ == 'g' else self.number('I',4)
                assert size <= 65536, 'DBus string bound'
                raw = self.take(size)
                assert b'\0' not in raw and self.take(1) == b'\0', 'DBus string terminator'
                value = raw.decode('ascii' if typ == 'g' else 'utf-8','strict')
                if typ == 'g': dbus_types(value)
                if typ == 'o': assert value.startswith('/') and (value == '/' or all(re.fullmatch(r'[A-Za-z0-9_]+',p) for p in value[1:].split('/'))), 'DBus object path'
                return value
            assert typ == 'v', 'unsupported DBus basic type'
            signature = self.value('g',depth+1); types_ = dbus_types(signature)
            assert len(types_) == 1, 'DBus variant must have one type'
            return {'signature':signature,'value':self.value(types_[0],depth+1)}
        kind, items = typ
        if kind == 'a':
            size = self.number('I',4)
            assert size <= LIMIT, 'DBus array bound'
            alignment = dbus_alignment(items); self.align(alignment)
            end = self.at+size; assert end <= self.end, 'partial DBus array'
            old_end = self.end; self.end = end
            values = []
            while self.at < end:
                values.append(self.value(items,depth+1))
                assert len(values) <= 1024, 'DBus array entry bound'
            self.end = old_end
            if type(items) is tuple and items[0] == '{':
                result = {}
                for key,value in values:
                    assert key not in result, 'duplicate DBus dictionary key'
                    result[key] = value
                return result
            return values
        assert kind in ('(','{'), 'unsupported DBus composite'
        self.align(8)
        return tuple(self.value(item,depth+1) for item in items)


def dbus_alignment(typ):
    if type(typ) is tuple: return 4 if typ[0] == 'a' else 8
    return {'y':1,'b':4,'n':2,'q':2,'i':4,'u':4,'x':8,'t':8,'d':8,'h':4,'s':4,'o':4,'g':1,'v':1}[typ]


class DBusBinaryStream:
    """dbus-monitor --binary emits ordinary messages, without PCAP headers."""
    def __init__(self, consume):
        self.consume = consume
        self.raw = b''
        self.total = self.count = 0

    def feed(self, chunk):
        self.total += len(chunk)
        assert self.total <= 16*LIMIT, 'complete DBus monitor stream bound'
        self.raw += chunk
        while len(self.raw) >= 16:
            assert self.raw[0] in (ord('l'),ord('B')), 'DBus byte order'
            endian = '<' if self.raw[0] == ord('l') else '>'
            typ, flags, version = self.raw[1:4]
            bodylen, serial, headers = struct.unpack(endian+'III',self.raw[4:16])
            assert 1 <= typ <= 4 and flags & ~7 == 0 and version == 1 and serial > 0, 'DBus fixed header'
            assert bodylen <= LIMIT and headers <= 65536, 'DBus frame budget'
            start = (16+headers+7)//8*8; length = start+bodylen
            if len(self.raw) < length: break
            raw, self.raw = self.raw[:length],self.raw[length:]
            reader = DBusValueReader(raw,endian,12,16+headers)
            fields = reader.value(('a',('(',('y','v'))))
            assert reader.at == 16+headers, 'DBus header boundary'
            header = {}
            expected = {1:'o',2:'s',3:'s',4:'s',5:'u',6:'s',7:'s',8:'g',9:'u'}
            for code, variant in fields:
                assert code in expected and code not in header and variant['signature'] == expected[code], 'unknown/duplicate DBus header field'
                header[code] = variant['value']
            assert not any(raw[16+headers:start]), 'DBus body alignment'
            if typ == 1: assert {1,3} <= set(header), 'DBus method-call headers'
            if typ in (2,3): assert 5 in header and header[5] > 0, 'DBus reply serial'
            if typ == 3: assert 4 in header, 'DBus error header'
            if typ == 4: assert {1,2,3} <= set(header), 'DBus signal headers'
            signature = header.get(8,'')
            body_reader = DBusValueReader(raw,endian,start)
            values = [body_reader.value(t) for t in dbus_types(signature)]
            assert body_reader.at == length, 'DBus trailing body bytes/signature mismatch'
            self.count += 1; assert self.count <= 4096, 'DBus message count'
            self.consume({'type':typ,'serial':serial,'header':header,'signature':signature,'body':values})
        assert len(self.raw) <= LIMIT+65552, 'DBus partial frame bound'

    def finish(self):
        assert not self.raw, 'partial DBus monitor frame at join'


class NotificationsExchange:
    """Pure matching logic. Identity/GUID are established by the real session."""
    def __init__(self, owner):
        assert re.fullmatch(r':\d+\.\d+',owner), 'actual Notifications unique owner required'
        self.owner = owner
        self.pending = {}
        self.ready = set()
        self.receipts = []

    def call(self, message, helper_name):
        h = message['header']
        assert message['type'] == 1 and h.get(7) == helper_name, 'wrong helper sender'
        method = h.get(3)
        if method == 'NameHasOwner':
            assert h.get(6) == 'org.freedesktop.DBus' and h.get(2) == 'org.freedesktop.DBus' and h.get(1) == '/org/freedesktop/DBus'
            assert message['signature'] == 's' and message['body'] == ['org.freedesktop.Notifications']
        else:
            assert method in ('GetCapabilities','Notify') and h.get(6) in ('org.freedesktop.Notifications',self.owner)
            assert h.get(2) == 'org.freedesktop.Notifications' and h.get(1) == '/org/freedesktop/Notifications'
            if method == 'GetCapabilities': assert message['signature'] == '' and message['body'] == []
            if method == 'Notify':
                assert (helper_name,'NameHasOwner') in self.ready and (helper_name,'GetCapabilities') in self.ready, 'Notify before real readiness replies'
                assert message['signature'] == 'susssasa{sv}i', 'Notify argument signature'
                b = message['body']
                assert len(b) == 8 and b[:6] == ['agent-notifications',0,'','Cursor CLI','Cursor CLI is stopping',[]], 'Notify actual source arguments'
                assert b[6] == {'suppress-sound':{'signature':'b','value':True}} and type(b[7]) is int and 1 <= b[7] <= 15000
        key = (helper_name,message['serial'])
        assert key not in self.pending, 'duplicate DBus call serial'
        self.pending[key] = {'method':method,'call':message}

    def reply(self, message):
        h = message['header']; key = (h.get(6),h.get(5))
        assert key in self.pending, 'wrong reply serial/destination'
        pending = self.pending.pop(key); method = pending['method']
        assert message['type'] == 2, 'DBus ERROR/partial/missing reply is uncertainty'
        sender = 'org.freedesktop.DBus' if method == 'NameHasOwner' else self.owner
        assert h.get(7) == sender, 'reply from foreign/replaced Notifications owner'
        if method == 'NameHasOwner': assert message['signature'] == 'b' and message['body'] == [True], 'NameHasOwner not true'
        if method == 'GetCapabilities':
            assert message['signature'] == 'as' and len(message['body']) == 1 and type(message['body'][0]) is list
            assert all(type(s) is str for s in message['body'][0]), 'invalid GetCapabilities reply'
        if method == 'Notify':
            assert message['signature'] == 'u' and len(message['body']) == 1 and type(message['body'][0]) is int and 0 <= message['body'][0] <= 4294967295, 'missing actual uint32 notification ID'
            self.receipts.append({'sender':key[0],'callSerial':key[1],'notificationID':message['body'][0],
                                  'call':pending['call'],'reply':message})
        else: self.ready.add((key[0],method))

    def finish(self):
        assert not self.pending, 'missing notification readiness/submission reply'


class OwnedNotificationsSession:
    """Standard services in P0/N1/U0; no process enters the native PID namespace."""
    def __init__(self, controller, root, clock, tools, display_number):
        self.controller, self.root, self.clock, self.tools = controller,pathlib.Path(root),clock,tools
        self.error = None
        self.processes = []
        self.control_processes = []
        self.pumps = []
        self.messages = []
        self.control_names = set()
        self.credentials = {}
        self.rpc_lock = threading.RLock()
        self.records_lock = threading.RLock()
        self.records_changed = threading.Condition(self.records_lock)
        self.trace_failure = None
        self.credential_receipts = {}
        self.active_names = set()
        self.exchange = None
        self.closing = False
        self.netfd = os.open('/proc/'+str(controller['pid'])+'/ns/net',os.O_RDONLY)
        assert 'net:['+str(os.fstat(self.netfd).st_ino)+']' == controller['ns']['net'], 'pinned C netns handle'
        self.directory = self.root/'session'
        self.directory.mkdir(mode=0o700)
        os.chown(self.directory,1000,1000)
        self.address = 'unix:path='+str(self.directory/'bus')
        assert type(display_number) is int and 90 <= display_number <= 4096
        self.display = ':'+str(display_number)
        self.xsocket = pathlib.Path('/tmp/.X11-unix')/('X'+str(display_number))
        self.xlock = pathlib.Path('/tmp')/('.X'+str(display_number)+'-lock')
        assert not self.xsocket.exists() and not self.xlock.exists(), 'accepted display already in use; no retry'
        self.xauthority = self.directory/'Xauthority'
        self.env = {'HOME':str(self.root/'home'),'XDG_CONFIG_HOME':str(self.root/'home/.config'),
                    'XDG_CACHE_HOME':str(self.root/'cache'),'TMPDIR':str(self.root/'tmp'),
                    'PATH':'/usr/bin:/bin','LANG':'C.UTF-8','DBUS_SESSION_BUS_ADDRESS':self.address,
                    'DISPLAY':self.display,'XAUTHORITY':str(self.xauthority)}
        cookie = os.urandom(16).hex()  # TEST display cookie, never an ambient credential.
        self.command('xauth',['-f',str(self.xauthority)],input=f'add {self.display} MIT-MAGIC-COOKIE-1 {cookie}\n'.encode('ascii'))
        self.display_process = self.start('Xvfb',[self.display,'-screen','0','800x600x24','-nolisten','tcp','-auth',str(self.xauthority)])
        self.bus = self.start('dbus-daemon',['--session','--nofork','--nopidfile','--address='+self.address,'--print-address=1'])
        self.wait(lambda:(self.directory/'bus').exists(),5)
        sock = (self.directory/'bus').lstat()
        assert stat.S_ISSOCK(sock.st_mode) and sock.st_uid == sock.st_gid == 1000, 'owned TEST bus socket'
        self.socket_identity = {'inode':sock.st_ino,'device':sock.st_dev,'mode':sock.st_mode,'uid':sock.st_uid,'gid':sock.st_gid}
        self.monitor = self.start('dbus-monitor',['--address',self.address,'--binary'],binary=True)
        self.stream = DBusBinaryStream(self.message)
        self.monitor_pump = threading.Thread(target=self.monitor_read,daemon=True)
        self.monitor_pump.start()
        self.dunst = self.start('dunst',['-config','/dev/null'])
        self.wait(lambda:self.xsocket.exists() and self.xlock.exists(),5)
        xs = self.xsocket.lstat(); xl = self.xlock.lstat()
        assert stat.S_ISSOCK(xs.st_mode) and xs.st_uid == xs.st_gid == 1000 and xl.st_uid == xl.st_gid == 1000
        self.display_identity = {'socket':{'inode':xs.st_ino,'device':xs.st_dev,'mode':xs.st_mode},
                                 'lock':{'inode':xl.st_ino,'device':xl.st_dev,'mode':xl.st_mode}}
        # Dunst owner first.
        self.wait(lambda:any(m['type'] == 4 and m['header'].get(3) == 'NameOwnerChanged' and
            m['body'][:1] == ['org.freedesktop.Notifications'] and len(m['body']) == 3 and m['body'][2]
            for m in self.messages),5)
        self.guid,self.guid_call = self.rpc('org.freedesktop.DBus.GetId',[], 's')
        assert re.fullmatch(r'[0-9a-f]{32}',self.guid), 'actual owned bus GUID'
        self.owner,self.owner_call = self.rpc('org.freedesktop.DBus.GetNameOwner',['string:org.freedesktop.Notifications'],'s')
        self.exchange = NotificationsExchange(self.owner)
        pid,_ = self.rpc('org.freedesktop.DBus.GetConnectionUnixProcessID',['string:'+self.owner],'u')
        uid,_ = self.rpc('org.freedesktop.DBus.GetConnectionUnixUser',['string:'+self.owner],'u')
        assert pid == self.dunst['identity']['pid'] and uid == 1000, 'real Notifications owner/daemon PID'
        self.credentials[self.owner] = proc_identity(pid,self.dunst['identity'])
        exists, name_call = self.rpc('org.freedesktop.DBus.NameHasOwner',['string:org.freedesktop.Notifications'],'b')
        assert exists is True
        capabilities, caps_call = self.rpc('org.freedesktop.Notifications.GetCapabilities',[], 'as',notifications=True)
        assert type(capabilities) is list and all(type(c) is str for c in capabilities)
        # Monitor roundtrips.
        for call in (self.guid_call,self.owner_call,name_call,caps_call): self.observed_roundtrip(call)
        self.assert_continuous()
        (self.root/'evidence/session-readiness.json').write_text(json.dumps({
            'busGUID':self.guid,'notificationsOwner':self.owner,'daemon':self.dunst['identity'],
            'socket':self.socket_identity,'display':self.display,'displayIdentity':self.display_identity,
            'processes':[p['identity'] for p in self.processes],'tools':tools,
            'actualControlRoundtrips':[self.guid_call,self.owner_call,name_call,caps_call],
            'GUIVisibility':'NOT_RUN'},indent=2)+'\n')

    def argv(self, name, args):
        return [self.tools['nsenter']['path'],'--net=/proc/self/fd/'+str(self.netfd),
                self.tools['setpriv']['path'],'--reuid=1000','--regid=1000','--clear-groups',
                self.tools[name]['path'],*args]

    def wait(self, predicate, cap):
        end = time.monotonic()+self.clock.wait(cap)
        while not predicate():
            assert self.error is None, self.error
            for p in self.processes: assert p['child'].poll() is None, 'owned standard session process exited'
            assert time.monotonic() < end, 'session/readiness absolute deadline'
            time.sleep(min(.01,max(0,end-time.monotonic())))

    def live(self, child, name, args, identity, end):
        while time.monotonic() < end:
            assert child.poll() is None, 'standard process failed before identity'
            if os.readlink(pathlib.Path('/proc')/str(child.pid)/'exe') != self.tools[name]['path']:
                time.sleep(min(.001,max(0,end-time.monotonic())))
                continue
            facts = proc_identity(child.pid,identity,startup=True)
            if facts is None:
                time.sleep(min(.001,max(0,end-time.monotonic())))
                continue
            if facts['exe'] == self.tools[name]['path']:
                if facts['argv'] != [self.tools[name]['path'],*args]:
                    print('standard-argv-rejected', child.pid, facts['birth'], facts['exe'], facts['uids'],
                          len(facts['argv']), [v[:120] for v in facts['argv'][:2]],
                          len(args)+1, [v[:120] for v in [self.tools[name]['path'],*args[:1]]], file=sys.stderr, flush=True)
                assert facts['argv'] == [self.tools[name]['path'],*args], 'actual standard process argv'
                assert facts['uids'] == facts['gids'] == [1000]*4 and facts['groups'] == []
                assert facts['ns']['pid'] == os.readlink('/proc/self/ns/pid') and facts['ns']['pid'] != self.controller['ns']['pid']
                assert facts['ns']['net'] == self.controller['ns']['net'] and facts['ns']['user'] == self.controller['ns']['user']
                assert facts['uidMap'] == self.controller['uidMap'] and facts['gidMap'] == self.controller['gidMap']
                assert facts['ppid'] == os.getpid(), 'outer service ownership'
                assert digest(facts['exe']) == self.tools[name]['sha256']
                return facts
            time.sleep(min(.001,max(0,end-time.monotonic())))
        raise IncompleteInstalledContract('standard session identity not established')

    def diagnostic_pump(self, pipe, path):
        def drain():
            try:
                with path.open('xb') as f:
                    size = 0
                    while True:
                        chunk = pipe.read(8192)
                        if not chunk: break
                        size += len(chunk); assert size <= LIMIT, 'session diagnostic bytes'
                        f.write(chunk)
            except Exception as error: self.error = type(error).__name__+':'+str(error)[:240]
            finally: pipe.close()
        t = threading.Thread(target=drain,daemon=True); self.pumps.append(t); t.start()

    def start(self, name, args, binary=False):
        end = time.monotonic()+self.clock.wait(1)
        child = subprocess.Popen(self.argv(name,args),stdin=subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,
            pass_fds=(self.netfd,),env=self.env,cwd=self.root/'workspace',start_new_session=True,bufsize=0)
        # Pre-exec birth/ppid/sid.
        record = {'name':name,'child':child,'identity':startup_identity(child.pid)}
        self.processes.append(record)
        self.diagnostic_pump(child.stderr,self.root/'evidence'/('session-'+name+'.stderr'))
        facts = self.live(child,name,args,record['identity'],end)
        assert facts['birth'] == record['identity']['birth'], 'standard process birth changed across exec'
        record['identity'] = facts
        if not binary: self.diagnostic_pump(child.stdout,self.root/'evidence'/('session-'+name+'.stdout'))
        return record

    def command(self, name, args, input=None):
        held = name == 'dbus-send'
        try:
            if held:
                assert proc_identity(self.bus['identity']['pid'],self.bus['identity']) == self.bus['identity'], 'owned bus changed before hold'
                os.kill(self.bus['identity']['pid'],signal.SIGSTOP)
                self.wait(lambda:(pathlib.Path('/proc')/str(self.bus['identity']['pid'])/'stat').read_text().split(') ')[-1].split()[0] == 'T',1)
            end = time.monotonic()+self.clock.wait(1)
            child = subprocess.Popen(self.argv(name,args),stdin=subprocess.PIPE if input is not None else subprocess.DEVNULL,stdout=subprocess.PIPE,stderr=subprocess.PIPE,
                pass_fds=(self.netfd,),env=self.env,cwd=self.root/'workspace',start_new_session=True)
            record = {'name':name,'child':child,'identity':startup_identity(child.pid)}
            self.control_processes.append(record)
            facts = self.live(child,name,args,record['identity'],end)
            assert facts['birth'] == record['identity']['birth'], 'control process birth changed across exec'
            record['identity'] = facts
        finally:
            if held:
                assert proc_identity(self.bus['identity']['pid'],self.bus['identity']) == self.bus['identity'], 'owned bus changed before resume'
                os.kill(self.bus['identity']['pid'],signal.SIGCONT)
        output,error = child.communicate(input=input,timeout=self.clock.wait(1))
        assert child.returncode == 0 and len(output) <= 65536 and len(error) <= 65536, 'standard session control failed'
        assert len(self.control_processes) <= 128, 'bounded separate control connection count'
        (self.root/'evidence/session-controls.json').write_text(json.dumps([
            {'name':p['name'],'identity':p['identity'],'exit':p['child'].returncode,
             'provenance':self.tools[p['name']]} for p in self.control_processes],indent=2)+'\n')
        return output


    def rpc(self, method, args, signature, notifications=False):
        with self.rpc_lock:
            name = 'org.freedesktop.Notifications' if notifications else 'org.freedesktop.DBus'
            path = '/org/freedesktop/Notifications' if notifications else '/org/freedesktop/DBus'
            reply_timeout = max(1,int(self.clock.wait(1)*1000))
            stdout = self.command('dbus-send',['--bus='+self.address,'--type=method_call','--print-reply',
                '--reply-timeout='+str(reply_timeout),'--dest='+name,path,method,*args]).decode('utf-8','strict')
            lines = stdout.splitlines()
            assert lines and lines[0].startswith('method return '), 'control DBus ERROR/unknown response'
            match = re.search(r'sender=(\S+) -> destination=(:\d+\.\d+).*reply_serial=(\d+)',lines[0])
            assert match, 'control reply headers'
            sender,destination,serial = match[1],match[2],int(match[3])
            self.control_names.add(destination)
            assert sender == (self.owner if notifications else 'org.freedesktop.DBus'), 'control owner drift'
            if signature == 's':
                assert len(lines) == 2 and lines[1].strip().startswith('string ')
                value = json.loads(lines[1].strip()[7:])
            elif signature == 'u':
                assert len(lines) == 2 and re.fullmatch(r'\s*uint32 \d+',lines[1])
                value = int(lines[1].split()[1])
            elif signature == 'b':
                assert len(lines) == 2 and lines[1].strip() in ('boolean true','boolean false')
                value = lines[1].strip() == 'boolean true'
            else:
                assert signature == 'as' and lines[1].strip() == 'array [' and lines[-1].strip() == ']'
                value = []
                for line in lines[2:-1]:
                    assert line.strip().startswith('string ')
                    value.append(json.loads(line.strip()[7:]))
            return value,{'sender':destination,'callSerial':serial,'replySender':sender,'method':method,
                          'arguments':list(args),'signature':signature,'value':value}

    def roundtrip_present(self, control):
        with self.records_lock:
            calls = [m for m in self.messages if m['type'] == 1 and m['header'].get(7) == control['sender'] and m['serial'] == control['callSerial']]
            replies = [m for m in self.messages if m['type'] in (2,3) and m['header'].get(6) == control['sender'] and m['header'].get(5) == control['callSerial']]
            if not calls or not replies: return False
            assert len(calls) == len(replies) == 1 and replies[0]['type'] == 2, 'monitor control reply ambiguity'
            assert calls[0]['header'].get(3) == control['method'].rsplit('.',1)[1]
            assert all(arg.startswith('string:') for arg in control['arguments']), 'closed original control argument types'
            assert calls[0]['signature'] == 's'*len(control['arguments']) and calls[0]['body'] == [
                arg.split(':',1)[1] for arg in control['arguments']], 'original monitored credential name arguments'
            assert replies[0]['header'].get(7) == control['replySender']
            assert replies[0]['signature'] == control['signature'] and replies[0]['body'] == [control['value']]
            return True

    def observed_roundtrip(self, control):
        self.wait(lambda:self.roundtrip_present(control),1)

    def held_credentials(self, capture, conn):
        """Independent dispatcher; the monitor never waits for its own records."""
        helper = capture['identity']
        key = (helper['pid'],helper['birth'])
        def ready():
            if self.error is not None or self.trace_failure is not None:
                raise AssertionError(self.error or self.trace_failure)
            names = [name for name,facts in self.credentials.items() if name in self.active_names and (facts['pid'],facts['birth']) == key]
            assert len(names) <= 1, 'ambiguous helper unique names'
            if not names: return False
            name = names[0]
            receipts = self.credential_receipts.get(name)
            if receipts is None or not all(self.roundtrip_present(call) for call in receipts): return False
            hellos = [m for m in self.messages if m['type'] == 1 and m['header'].get(7) == name and
                      m['header'].get(2) == 'org.freedesktop.DBus' and m['header'].get(3) == 'Hello']
            assert len(hellos) <= 1, 'ambiguous helper Hello'
            if not hellos: return False
            replies = [m for m in self.messages if m['type'] in (2,3) and m['header'].get(6) == name and m['header'].get(5) == hellos[0]['serial']]
            if not replies: return False
            assert len(replies) == 1 and replies[0]['type'] == 2 and replies[0]['signature'] == 's' and replies[0]['body'] == [name], 'failed/ambiguous Hello'
            if conn['outcome'] is None: return False
            assert conn['connected'], 'connect intent/outcome is not connection proof'
            assert receipts[0]['value'] == helper['pid'] and receipts[1]['value'] == 1000, 'original credential RPC values'
            assert [call['method'] for call in receipts] == ['org.freedesktop.DBus.GetConnectionUnixProcessID',
                'org.freedesktop.DBus.GetConnectionUnixUser'] and all(call['arguments'] == ['string:'+name]
                for call in receipts), 'original credential RPC name/method incarnation'
            return name
        with self.records_changed:
            assert self.records_changed.wait_for(ready,self.clock.wait(1)), 'held credential/monitor deadline'
            name = ready()
            peer = self.credentials[name]
        # No trace/rpc/records lock spans a control RPC, Condition or channel wait.
        self.assert_continuous()
        current = proc_identity(helper['pid'],helper)
        assert current == helper == peer and current['tracer'] == capture['identity']['tracer'], 'fresh exact selected bus peer'
        assert len(capture['busConnections']) == 1 and capture['busConnections'][0] is conn, 'sole socket/name incarnation'
        conn['name'] = name
        return name

    def message(self, message):
        new_credential = None
        h = message['header']
        ownership = message['type'] == 4 and h.get(2) == 'org.freedesktop.DBus' and h.get(3) == 'NameOwnerChanged'
        with self.records_changed:
            # Publish the real departure and remove its ACK authority atomically.
            # The separate credential RPC below never holds the records lock.
            if ownership:
                assert h.get(7) == 'org.freedesktop.DBus' and h.get(1) == '/org/freedesktop/DBus', 'actual bus name authority'
                assert message['signature'] == 'sss' and len(message['body']) == 3
                name,old,new = message['body']
                if name.startswith(':'):
                    assert re.fullmatch(r':\d+\.\d+',name), 'actual unique bus name'
                    if old == '' and new == name: self.active_names.add(name)
                    elif old == name and new == '': self.active_names.discard(name)
                    else: raise AssertionError('unique bus name replacement')
            self.messages.append(message)
            assert len(self.messages) <= 4096 and len(json.dumps(self.messages)) <= 16*LIMIT, 'bounded TEST monitor records'
            self.records_changed.notify_all()
        if ownership:
            if name == 'org.freedesktop.Notifications' and self.exchange is not None:
                assert self.closing or old == '' and new == self.owner, 'Notifications owner loss/replacement'
            if name.startswith(':') and old == '' and new == name:
                with self.rpc_lock:
                    if name not in self.control_names:
                        pid,pid_call = self.rpc('org.freedesktop.DBus.GetConnectionUnixProcessID',['string:'+name],'u')
                        uid,uid_call = self.rpc('org.freedesktop.DBus.GetConnectionUnixUser',['string:'+name],'u')
                        assert uid == 1000, 'bus peer UID'
                        facts = proc_identity(pid)
                        assert facts['uids'] == facts['gids'] == [1000]*4 and facts['groups'] == []
                        assert facts['ns']['net'] == self.controller['ns']['net'] and facts['ns']['user'] == self.controller['ns']['user']
                        if facts['ns']['pid'] != self.controller['ns']['pid']:
                            assert any(p['identity']['pid'] == pid and p['identity']['birth'] == facts['birth'] for p in self.processes), 'unknown outer session peer'
                        else:
                            assert facts['argv'][1:4] == ['cursor-event','stop','--binding'] and len(facts['argv']) == 5, 'unknown native bus peer'
                        new_credential = (name,facts,(pid_call,uid_call))
            if name in self.credentials and old == name and new == '' and self.exchange is not None:
                assert not any(key[0] == name for key in self.exchange.pending), 'helper disconnect before matching reply'
        if self.exchange is not None:
            sender = h.get(7)
            credential = self.credentials.get(sender)
            if message['type'] == 1 and credential and credential['ns']['pid'] == self.controller['ns']['pid']:
                self.exchange.call(message,sender)
            if message['type'] in (2,3) and (h.get(6),h.get(5)) in self.exchange.pending:
                self.exchange.reply(message)
        with self.records_changed:
            if new_credential is not None:
                name,facts,receipts = new_credential
                assert name in self.active_names and name not in self.credentials, 'credential callback name incarnation'
                self.credentials[name] = facts
                self.credential_receipts[name] = receipts
            self.records_changed.notify_all()

    def monitor_read(self):
        try:
            pipe = self.monitor['child'].stdout
            while True:
                raw = pipe.read(8192)
                if not raw: break
                self.stream.feed(raw)
            self.stream.finish()
            assert self.closing, 'monitor continuity ended early'
        except Exception as error:
            with self.records_changed:
                if self.error is None: self.error = type(error).__name__+':'+str(error)[:240]
                self.records_changed.notify_all()
        finally: self.monitor['child'].stdout.close()

    def assert_continuous(self):
        assert self.error is None and self.monitor_pump.is_alive(), self.error or 'monitor missing'
        for p in self.processes:
            assert p['child'].poll() is None, 'session process not live'
            current = proc_identity(p['identity']['pid'],p['identity'])
            assert current['ns'] == p['identity']['ns'] and current['uids'] == current['gids'] == [1000]*4 and current['groups'] == []
        s = (self.directory/'bus').lstat()
        assert {'inode':s.st_ino,'device':s.st_dev,'mode':s.st_mode,'uid':s.st_uid,'gid':s.st_gid} == self.socket_identity

    def checkpoint(self, helper=None, enabled=False):
        self.assert_continuous()
        guid,call = self.rpc('org.freedesktop.DBus.GetId',[],'s')
        assert guid == self.guid
        self.observed_roundtrip(call)  # binary stream passed through this boundary.
        self.exchange.finish()
        if helper is not None:
            names = [name for name,f in self.credentials.items() if f['pid'] == helper['pid'] and f['birth'] == helper['birth']]
            assert len(names) == (1 if enabled else 0), 'actual helper sender identity missing/disabled helper reached bus'
            if enabled:
                receipts = [r for r in self.exchange.receipts if r['sender'] == names[0]]
                assert len(receipts) == 1, 'one correlated real Notify reply required'
                credential = self.credentials[names[0]]
                assert credential['nspid'] == helper['nspid'] and credential['ns'] == helper['ns'] and credential['argv'] == helper['argv']
        assert len(self.exchange.receipts) == 1, 'duplicate/disabled Notify effect'
        (self.root/'evidence/notifications-actual.json').write_text(json.dumps({
            'busGUID':self.guid,'owner':self.owner,'credentials':self.credentials,
            'receipts':self.exchange.receipts,'messages':self.messages,'GUIVisibility':'NOT_RUN'},indent=2)+'\n')

    def close(self):
        began = time.monotonic(); deadline = self.clock.cleanup_deadline()
        self.assert_continuous(); self.exchange.finish()
        assert all(p['child'].poll() == 0 for p in self.control_processes), 'separate control connection join incomplete'
        self.closing = True
        # Join native/RPC; close.
        for p in (self.monitor,self.dunst,self.bus,self.display_process):
            proc_identity(p['identity']['pid'],p['identity'])
            os.kill(p['identity']['pid'],signal.SIGTERM)
            p['child'].wait(timeout=max(.001,min(2,deadline-time.monotonic())))
        self.monitor_pump.join(timeout=max(0,deadline-time.monotonic()))
        for t in self.pumps: t.join(timeout=max(0,deadline-time.monotonic()))
        assert not self.monitor_pump.is_alive() and all(not t.is_alive() for t in self.pumps) and self.error is None, 'standard session join incomplete'
        self.stream.finish()
        os.close(self.netfd)
        self.clock.charge_cleanup(began)


class OuterObservation:
    def __init__(self, child, peer, inputs, clock):
        self.child, self.peer, self.inputs, self.clock = child,peer,inputs,clock
        self.controller = None
        self.trace = self.session = None
        self.sequence = 0
        self.nonce = None
        self.phases = []
        self.closed = False
        self.active_phase = None

    def controller_identity(self, local):
        assert local['pid'] == local['tgid'] == 1 and local['uids'] == local['gids'] == [1000]*4 and local['groups'] == []
        matches = []
        for p in pathlib.Path('/proc').iterdir():
            if not p.name.isdecimal(): continue
            try:
                if os.readlink(p/'ns/pid') != local['ns']['pid']: continue
                facts = proc_identity(int(p.name))
            except FileNotFoundError: continue
            if facts['birth'] == local['birth'] and facts['nspid'][-1] == 1: matches.append(facts)
        assert len(matches) == 1, 'one exact ancestor-visible C identity'
        actual = matches[0]
        assert actual['ppid'] == self.child.pid and actual['ns'] == local['ns']
        assert actual['uids'] == actual['gids'] == [1000]*4 and actual['groups'] == []
        assert actual['uidMap'] == local['uidMap'] and actual['gidMap'] == local['gidMap']
        assert actual['ns']['user'] == os.readlink('/proc/self/ns/user')
        assert actual['ns']['pid'] != os.readlink('/proc/self/ns/pid')
        assert actual['ns']['net'] != os.readlink('/proc/self/ns/net')
        return actual

    def empty_native(self):
        for p in pathlib.Path('/proc').iterdir():
            if not p.name.isdecimal(): continue
            try: same = os.readlink(p/'ns/pid') == self.controller['ns']['pid']
            except FileNotFoundError: continue
            if same: assert int(p.name) == self.controller['pid'], 'CLI/native descendants remain at phase boundary'

    def request(self):
        message = control_receive(self.peer,self.clock)
        assert message['sequence'] == self.sequence+1, 'outer control sequence'
        self.sequence += 1
        if self.nonce is None: self.nonce = message['nonce']
        assert message['nonce'] == self.nonce and re.fullmatch(r'[0-9a-f]{64}',self.nonce), 'case control nonce'
        self.clock.cleanup_used = max(self.clock.cleanup_used,message['cleanupUsed'])
        assert 0 <= self.clock.cleanup_used < 8, 'shared cumulative cleanup ledger'
        reply = {'sequence':self.sequence,'nonce':self.nonce,'ok':True}
        action = message['action']
        if action == 'controller-thread':
            expected = 'enabled-native' if not self.phases else 'disabled-restart'
            if self.trace is None or self.controller is None or self.closed or self.clock.work is None or len(self.phases) >= 2 or message.get('phase') != self.active_phase or self.active_phase != expected: raise AssertionError('controller thread inactive phase')
            local = message.get('thread')
            if type(local) is not dict or set(local) != {'tid','birth'} or type(local['tid']) is not int or not 0 < local['tid'] <= 2147483647 or type(local['birth']) is not int or not 0 <= local['birth'] <= 18446744073709551615: raise AssertionError('controller thread tuple budget')
            parser = self.trace.parser
            def matching():
                return [t for t in parser.tasks.values() if t.get('parent') and not t.get('exited') and not t.get('capture') and not t.get('nativeLeader') and t['key'][1] == local['birth'] and t['facts']['tgid'] == self.controller['pid'] and len(t['facts']['nspid']) == len(self.controller['nspid']) and t['facts']['nspid'][-1] == local['tid']]
            with parser.changed:
                if not parser.changed.wait_for(lambda:self.trace.error is not None or bool(matching()),self.clock.wait(1)): raise AssertionError('controller thread live trace deadline')
                known = matching()
                if self.trace.error is not None or len(known) != 1: raise AssertionError(self.trace.error or 'controller thread mapping ambiguity')
                task = known[0]; parent_key = task['parent']
                parent = parser.register(parent_key[0]); child = parser.register(task['key'][0])
                parent_fresh = trace_task_identity(parent_key[0]); child_fresh = trace_task_identity(task['key'][0])
                for fresh,retained in ((parent_fresh,parent),(child_fresh,child)):
                    if (fresh['pid'],fresh['birth']) != retained['key'] or fresh['ns'] != self.controller['ns'] or fresh['uids'] != fresh['gids'] or fresh['uids'] != [1000]*4 or fresh['groups'] != [] or fresh['tracer'] != self.trace.tracer['pid'] or fresh['nspid'] != retained['facts']['nspid']: raise AssertionError('controller thread fresh metadata drift')
                if parent['key'] != parent_key or parent_fresh['tgid'] != self.controller['pid'] or child['key'] != task['key'] or child['parent'] != parent_key or child_fresh['tgid'] != self.controller['pid'] or len(child_fresh['nspid']) != len(self.controller['nspid']) or child_fresh['nspid'][-1] != local['tid'] or child_fresh['birth'] != local['birth']: raise AssertionError('controller thread fresh identity drift')
                reply['controllerThread'] = {'tid':local['tid'],'birth':local['birth'],'phase':self.active_phase}
            control_send(self.peer,reply,self.clock)
            return
        if action == 'prepare':
            assert self.controller is None and self.trace is None and self.session is None
            self.controller = self.controller_identity(message['controller'])
            self.empty_native()
            installed = (pathlib.Path(message['primary']),pathlib.Path(message['selector']),message['binding'],message['generation'])
            # Allocate before containment.
            self.session = OwnedNotificationsSession.__new__(OwnedNotificationsSession)
            self.session.__init__(self.controller,self.inputs['root'],self.clock,self.inputs['tools'],self.inputs['displayNumber'])
            self.empty_native()  # All services are outside P1; no setup CLI remains.
            self.trace = PassiveStrace.__new__(PassiveStrace)
            self.trace.__init__(self.controller,installed,self.inputs['root'],self.clock,self.inputs['tools'],self.session)
            reply['environment'] = {k:self.session.env[k] for k in ('DBUS_SESSION_BUS_ADDRESS','DISPLAY','XAUTHORITY')}
            reply['controller'] = self.controller
            reply['tracer'] = self.trace.tracer
        elif action == 'begin-phase':
            assert message['phase'] == ('enabled-native' if not self.phases else 'disabled-restart') and len(self.phases) < 2
            self.empty_native(); self.session.assert_continuous(); self.trace.begin_phase()
            self.active_phase = message['phase']
        elif action == 'leader':
            assert message['phase'] == ('enabled-native' if not self.phases else 'disabled-restart')
            if self.clock.work is None: self.clock.work = min(message['nativeStart']+90,self.clock.outer-8)
            self.clock.wait(5)
            end = time.monotonic()+self.clock.wait(1)
            while True:
                assert self.trace.error is None, self.trace.error
                with self.trace.parser.lock:
                    known = [t for t in self.trace.parser.tasks.values() if t['facts']['birth'] == message['localLeader']['birth'] and t['facts']['nspid'][-1] == message['localLeader']['pid']]
                    if len(known) == 1: break
                assert time.monotonic() < end, 'trace/native leader synchronization deadline'
                time.sleep(min(.001,max(0,end-time.monotonic())))
            reply['leader'] = self.trace.select_leader(message['localLeader'],end)
            self.session.assert_continuous()
        elif action == 'phase':
            phase = message['phase']
            assert phase == ('enabled-native' if not self.phases else 'disabled-restart') and len(self.phases) < 2
            self.empty_native()
            end = time.monotonic()+self.clock.wait(1)
            while True:
                assert self.trace.error is None, self.trace.error
                with self.trace.parser.lock:
                    completed = [c for c in self.trace.parser.selected if c['leader'][1] == message['localLeader']['birth'] and c['exited'] and c['eof']]
                    terminal = all(t.get('exited') or t['facts']['tgid'] == self.controller['pid'] for t in self.trace.parser.tasks.values())
                    if len(completed) == 1 and terminal: break
                assert time.monotonic() < end, 'native exit/alias trace completion deadline'
                time.sleep(min(.001,max(0,end-time.monotonic())))
            capture,raw,facts = self.trace.phase(message['localLeader'],message['cleanupSignal'],message['cleanupRoles'])
            self.session.checkpoint(capture['identity'],enabled=phase == 'enabled-native')
            if self.phases:
                assert facts['conversation'] != self.phases[0]['facts']['conversation'] and facts['generation'] != self.phases[0]['facts']['generation'], 'planned restart has stale native identities'
            evidence = pathlib.Path(self.inputs['root'])/'evidence'/phase
            sha = hashlib.sha256(raw).hexdigest()
            (evidence/'native-outer-consumed-stdin.bin').write_bytes(raw)
            (evidence/'native-outer-consumed-stdin.sha256').write_text(sha+'\n')
            (evidence/'stdin-alias-trace.json').write_text(json.dumps({k:v for k,v in capture.items() if k not in ('bytes','busConnections')},indent=2)+'\n')
            self.phases.append({'phase':phase,'facts':facts,'nativeSHA256':sha,'helperIdentity':capture['identity']})
            reply.update(nativeBytes=base64.b64encode(raw).decode(),facts=facts,nativeSHA256=sha,helperIdentity=capture['identity'])
        elif action == 'checkpoint':
            assert self.phases
            self.empty_native(); self.session.checkpoint()
        elif action == 'close':
            assert len(self.phases) == 2
            self.empty_native(); self.session.checkpoint()
            self.trace.close(); self.session.close()
            self.closed = True
            reply['cleanupUsed'] = self.clock.cleanup_used
        else: raise AssertionError('unknown outer control action')
        control_send(self.peer,reply,self.clock)


def digest(path):
    with pathlib.Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def physical(path):
    path = pathlib.Path(path)
    assert path.is_absolute() and path.resolve(strict=True) == path, 'physical absolute input required'
    return path


def execution_inputs(filename, inside=False):
    data = strict_json(physical(filename).read_bytes())
    assert set(data) == {'sourceSHA', 'root', 'artifact', 'stage', 'package', 'cursorArchive', 'buildInfo', 'machineID', 'sha256', 'tools', 'displayNumber', 'readbackHelper', 'readbackBuildInfo'}, 'execution input fields'
    assert data['sourceSHA'] == SOURCE_SHA and data['machineID'], 'fixed final source/runner required'
    root = pathlib.Path(data['root'])
    assert root.is_absolute() and root.name.startswith('TEST-cursor-installed-') and (root.is_dir() if inside else not root.exists()) and not root.is_symlink(), 'fresh TEST root required'
    physical(root.parent)
    for key in ['artifact', 'stage', 'package', 'cursorArchive', 'buildInfo', 'readbackHelper', 'readbackBuildInfo']: physical(data[key])
    primary = pathlib.Path(data['stage'])/'claude-notifications-linux-amd64'
    assert digest(primary) == data['sha256']['primary'] and digest(data['package']) == data['sha256']['package']
    with zipfile.ZipFile(data['package']) as archive:
        names = archive.namelist()
        assert len(names) == len(set(names)) and {'plugin.json', 'mcp.json', 'bin/claude-notifications'} <= set(names)
        for member in archive.infolist():
            path = pathlib.PurePosixPath(member.filename)
            assert not path.is_absolute() and '..' not in path.parts and '\\' not in member.filename
            assert not stat.S_ISLNK(member.external_attr >> 16) and member.file_size <= 80 << 20
        assert sum(m.file_size for m in archive.infolist()) <= 96 << 20
        assert hashlib.sha256(archive.read('bin/claude-notifications')).hexdigest() == data['sha256']['primary']
    build_info = pathlib.Path(data['buildInfo']).read_bytes()
    assert len(build_info) <= 65536 and hashlib.sha256(build_info).hexdigest() == data['sha256']['buildInfo']
    assert ('vcs.revision='+SOURCE_SHA).encode() in build_info and b'vcs.modified=false' in build_info
    assert b'github.com/777genius/plugin-kit-ai/sdk\tv1.2.1-0.20261002230153-01f7fced8098' in build_info
    assert b'=>\t' not in build_info, 'local module replacement forbidden'
    assert digest(data['readbackHelper']) == data['sha256']['readbackHelper'], 'source-built read-only bridge bytes'
    bridge_info = pathlib.Path(data['readbackBuildInfo']).read_bytes()
    assert len(bridge_info) <= 65536 and hashlib.sha256(bridge_info).hexdigest() == data['sha256']['readbackBuildInfo']
    assert ('vcs.revision='+SOURCE_SHA).encode() in bridge_info and b'vcs.modified=false' in bridge_info
    assert ('main.packetSourceSHA256='+READBACK_SOURCE_SHA256).encode() in bridge_info and b'=>\t' not in bridge_info
    assert hashlib.sha256(READBACK_SOURCE_BYTES).hexdigest() == READBACK_SOURCE_SHA256
    archive = pathlib.Path(data['cursorArchive'])
    dist = pathlib.Path(data['artifact'])
    assert archive.stat().st_size == 182618248 and digest(archive) == CURSOR_ARCHIVE_SHA
    with tarfile.open(archive) as opened:
        members = opened.getmembers()
        assert len(members) == 580
        assert len({m.name for m in members}) == len(members)
        for member in members:
            name = pathlib.PurePosixPath(member.name)
            assert not name.is_absolute() and '..' not in name.parts and (member.isfile() or member.isdir())
        files = [m for m in members if m.isfile()]
        paths = [pathlib.PurePosixPath(m.name).parts for m in files]
        strip = 1 if len({parts[0] for parts in paths}) == 1 and all(len(parts) > 1 for parts in paths) else 0
        expected = set()
        for member in files:
            relative = pathlib.Path(*pathlib.PurePosixPath(member.name).parts[strip:])
            assert str(relative) not in expected
            expected.add(str(relative))
            target = physical(dist/relative)
            assert target.is_file()
            assert target.read_bytes() == opened.extractfile(member).read()
            assert target.stat().st_mode & 0o111 == member.mode & 0o111
        actual = set()
        for target in dist.rglob('*'):
            assert not target.is_symlink() and (target.is_dir() or stat.S_ISREG(target.lstat().st_mode))
            if target.is_file(): actual.add(str(target.relative_to(dist)))
        assert actual == expected, 'complete fixed runtime tree required'
    assert digest(pathlib.Path(data['artifact'])/'cursor-agent') == LAUNCHER_SHA
    assert digest(pathlib.Path(data['artifact'])/'index.js') == INDEX_SHA
    for pin in ARTIFACT_PINS:
        assert digest(pathlib.Path(data['artifact'])/pin['file']) == pin['sha256']
    return data


def require_native_observation_contract(inputs, clock, inside=False):
    """Checks grant prerequisites only; readiness is a later actual roundtrip."""
    raise IncompleteInstalledContract('R1360 TEST increment: full Linux proof/exact review/carrier qualification pending; native ineligible')
    clock.wait(5)
    assert inputs['sourceSHA'] == SOURCE_SHA, 'immutable current helper source'
    assert hashlib.sha256(WIRE_BYTES).hexdigest() == WIRE_SHA, 'original wire protection'
    assert set(inputs['tools']) == {'strace','dbus-daemon','dbus-monitor','dbus-send','dunst','Xvfb','xauth','nsenter','setpriv'}
    for name, tool in inputs['tools'].items():
        assert set(tool) == {'path','sha256','package','version','installedMetadataSHA256'}, 'actual standard package provenance fields'
        path = physical(tool['path'])
        assert path == pathlib.Path('/usr/bin')/name and path.is_file(), 'standard immutable tool path'
        assert digest(path) == tool['sha256'] and stat.S_IMODE(path.stat().st_mode) & 0o111, 'actual standard binary bytes'
        assert tool['package'] and tool['version'] and re.fullmatch(r'[0-9a-f]{64}',tool['installedMetadataSHA256'])
    assert inputs['tools']['strace']['version'] == '6.8-0ubuntu2', 'source-qualified strace version'
    assert inputs['tools']['strace']['package'] == 'strace'
    if inside:
        # P1 root checks/drop; setup/passive attach.
        assert os.getpid() == 1 and os.geteuid() == 0
        return
    assert os.geteuid() == 0, 'outer capability owner required'
    status = pathlib.Path('/proc/self/status').read_text()
    values = dict(line.split(':',1) for line in status.splitlines() if ':' in line)
    capabilities = int(values['CapEff'].strip(),16)
    assert capabilities & (1<<19) and capabilities & (1<<21), 'existing SYS_PTRACE/SYS_ADMIN required'
    assert values['Seccomp'].strip() == '0', 'unknown outer syscall filter permission'
    assert pathlib.Path('/proc/sys/kernel/yama/ptrace_scope').read_text().strip() in ('0','1','2'), 'existing Yama attach permission'
    assert pathlib.Path('/proc/self/attr/current').read_text().strip() == 'unconfined', 'unknown LSM permission'
    for name, tool in inputs['tools'].items():
        command = ['/usr/bin/dpkg-query','--show','--showformat=${binary:Package}\t${Version}\t${db:Status-Status}\n',tool['package']]
        metadata = subprocess.check_output(command,timeout=clock.wait(5),env={'PATH':'/usr/bin:/bin','LANG':'C'})
        assert metadata.decode().strip() == tool['package']+'\t'+tool['version']+'\tinstalled'
        assert hashlib.sha256(metadata).hexdigest() == tool['installedMetadataSHA256'], 'actual installed package metadata'


def qualified_runner(profile, machine_id):
    assert pathlib.Path('/etc/machine-id').read_text().strip() == machine_id, 'actual frozen runner identity'
    assert platform.system() == 'Linux' and platform.machine() == 'x86_64'
    assert pathlib.Path('/proc/sys/kernel/osrelease').read_text().strip() == '6.17.0-1022-azure'
    release = pathlib.Path('/etc/os-release').read_text()
    assert '\nID=ubuntu\n' in '\n'+release and 'VERSION="24.04.5 LTS' in release
    mounts = pathlib.Path('/proc/self/mountinfo').read_text()
    assert len(mounts.encode()) <= LIMIT
    decode = lambda s: s.replace('\\040', ' ').replace('\\011', '\t').replace('\\012', '\n').replace('\\134', '\\')
    for path in [profile, *profile.parents]:
        matches = []
        for row in mounts.splitlines():
            left, sep, right = row.partition(' - ')
            if not sep or len(left.split()) < 6 or len(right.split()) < 3: continue
            mount = decode(left.split()[4])
            if mount == '/' or str(path) == mount or str(path).startswith(mount+'/'):
                matches.append((len(mount), right.split()[0]))
        longest = max(n for n, fs in matches)
        selected = [fs for n, fs in matches if n == longest]
        assert selected == ['ext4'], 'every actual ancestor must be unambiguously ext4'
    # No grants/foreign/token; capture+revalidate.


def case_env(root):
    return {'HOME':str(root/'home'), 'XDG_CONFIG_HOME':str(root/'home/.config'),
            'TMPDIR':str(root/'tmp'), 'XDG_CACHE_HOME':str(root/'cache'),
            'AGENT_NOTIFICATIONS_CONFIG':str(root/'home/.config/agent-notifications/config.json'),
            'PATH':'/usr/bin:/bin', 'LANG':'C.UTF-8', **SESSION_ENV}


def cli(root, primary, argv, name, payload=None, terminal=False):
    command = [str(primary), *argv]
    if terminal:
        master, slave = os.openpty()
        def controlling_pty():
            os.setsid()
            fcntl.ioctl(0, termios.TIOCSCTTY, 0)
        child = None
        raw = bytearray()
        answered = False
        deadline = time.monotonic()+CASE_CLOCK.wait(30)
        try:
            child = subprocess.Popen(command, stdin=slave, stdout=slave, stderr=slave,
                cwd=root/'workspace', env=case_env(root), preexec_fn=controlling_pty)
            os.close(slave)
            slave = None
            while child.poll() is None:
                assert time.monotonic() < deadline, 'real controlling-PTY confirmation deadline'
                if not select.select([master], [], [], min(.05,CASE_CLOCK.wait(.05)))[0]: continue
                try: part = os.read(master, 8192)
                except OSError as error:
                    if error.errno == 5: break
                    raise
                raw.extend(part)
                assert len(raw) <= LIMIT, 'bounded confirmation output'
                if not answered and b'Apply this plan?' in raw:
                    # ONE fresh Yes via PTY.
                    os.write(master, b'y\n')
                    answered = True
            assert answered and child.wait(timeout=min(CASE_CLOCK.wait(30),max(.001,deadline-time.monotonic()))) == 0
            result = subprocess.CompletedProcess(command, child.returncode)
            (root/'evidence'/str(name+'.pty')).write_bytes(raw)
        finally:
            if slave is not None: os.close(slave)
            os.close(master)
            # PTY: no retry; contain unknown children.
            if child is not None and child.poll() is None: raise IncompleteInstalledContract('PTY join incomplete')
    else:
        result = subprocess.run(command, input=payload, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                cwd=root/'workspace', env=case_env(root), timeout=CASE_CLOCK.wait(30))
        assert len(result.stdout) <= LIMIT and len(result.stderr) <= LIMIT
        (root/'evidence'/str(name+'.stdout')).write_bytes(result.stdout)
        (root/'evidence'/str(name+'.stderr')).write_bytes(result.stderr)
    assert result.returncode == 0, 'public CLI failed; do not retry'
    return result.stdout if not terminal else None


def wizard(root, primary, artifact, package, action, enabled, extra=(), json_output=True):
    control = root/'home/.config/agent-notifications'
    args = ['setup-notifications', 'wizard', '--action', action, '--agents', 'cursor',
            '--hooks', 'false', '--agent-notify', str(enabled).lower(), '--control-root', str(control),
            '--scope-root', str(root/'home/.cursor'), '--client-executable', str(artifact/'cursor-agent'), '--yes']
    if package is not None:
        ledger = strict_json((control/'ownership.json').read_bytes())
        args += ['--package', str(package), '--runtime-root', ledger['RuntimeRoot'],
                 '--global-config', case_env(root)['AGENT_NOTIFICATIONS_CONFIG']]
    args += list(extra)
    if json_output: args += ['--json']
    return args


def inventory_object_refusal(directory, current, facts):
    """Best-effort bounded TEST-only metadata from the already-read lstat."""
    try:
        test_root = next(p for p in (directory, *directory.parents) if p.name.startswith('TEST-'))
        relative = current.relative_to(test_root)
        assert not relative.is_absolute() and '..' not in relative.parts
        path = str(relative)
        row = {'path':path[:512], 'pathTruncated':len(path) > 512,
               'pathSHA256':hashlib.sha256(path.encode('utf-8','surrogatepass')).hexdigest(),
               'mode':int(facts.st_mode), 'type':stat.S_IFMT(facts.st_mode),
               'device':int(facts.st_dev), 'inode':int(facts.st_ino)}
        assert all(0 <= row[k] < 2**64 for k in ('mode','type','device','inode'))
        message = json.dumps(row,ensure_ascii=True,separators=(',',':'))
        assert len(message) <= 4096
        print('inventory-object-refusal '+message,file=sys.stderr,flush=True)
    except Exception:
        pass


def vendor_worker_socket_path(root):
    """Lexical pinned45d9 path derivation; only this isolated TEST case."""
    root = pathlib.Path(root)
    assert root.name == 'TEST-cursor-installed-one' and root.parent.name.startswith('TEST-cursor-installed-packet-')
    assert root.parent.parent == pathlib.Path('/tmp') and '..' not in root.parts
    data = root/'home/.cursor'
    base = data/'projects'
    if len(str(base)) > 84: base = data
    if len(str(base)) > 84: return None  # vendor /tmp/.cursor fallback is outside this owned TEST root
    workspace = re.sub(r'^-+|-+$','',re.sub(r'-+','-',re.sub(r'[^a-zA-Z0-9]','-',str(root/'workspace'))))
    joined = str(base/workspace)
    if len(joined) > 92: joined = joined[:84]+'-'+hashlib.sha256(joined.encode()).hexdigest()[:7]
    socket_path = pathlib.Path(joined)/'worker.sock'
    return socket_path if socket_path.is_relative_to(data) else None


def bounded_tree(directory, vendor_root=None):
    """Full no-follow path, member, byte and identity sets; no suffix guessing."""
    directory = pathlib.Path(directory)
    result = {}; total = 0
    vendor_socket = vendor_worker_socket_path(vendor_root) if vendor_root is not None else None
    if not os.path.lexists(directory): return result
    stack = [directory]
    while stack:
        current = stack.pop()
        facts = current.lstat()
        assert len(result) < 16384, 'full object membership bound'
        value = {'mode':facts.st_mode, 'uid':facts.st_uid, 'gid':facts.st_gid,
                 'device':facts.st_dev,'inode':facts.st_ino,'nlink':facts.st_nlink}
        if stat.S_ISLNK(facts.st_mode):
            value['link'] = os.readlink(current)
            assert len(value['link'].encode()) <= 4096, 'raw link bound'
        elif stat.S_ISREG(facts.st_mode):
            assert facts.st_size <= TEST_RUNTIME_FILE_LIMIT, 'full object file bound'
            body = current.read_bytes(); assert len(body) == facts.st_size
            total += len(body); assert total <= 1024**3, 'full writable-case bound'
            value['sha256'] = hashlib.sha256(body).hexdigest(); value['byteLength'] = len(body)
            if current.suffix == '.json':
                parsed = strict_json(body)
                value['jsonMembers'] = json_members(parsed)
        elif stat.S_ISSOCK(facts.st_mode) and current == vendor_socket:
            parent = result.get(str(current.parent.relative_to(directory)),{})
            assert (facts.st_uid,facts.st_gid,facts.st_dev) == (parent.get('uid'),parent.get('gid'),parent.get('device')), 'foreign vendor socket ownership'
            value['type'] = stat.S_IFSOCK
        else:
            if not stat.S_ISDIR(facts.st_mode):
                inventory_object_refusal(directory,current,facts)
            assert stat.S_ISDIR(facts.st_mode), 'unknown filesystem object'
            stack.extend(sorted(current.iterdir(),reverse=True))
        final = current.lstat()
        assert (facts.st_mode,facts.st_uid,facts.st_gid,facts.st_dev,facts.st_ino,facts.st_nlink,facts.st_size,facts.st_mtime_ns,facts.st_ctime_ns) == (final.st_mode,final.st_uid,final.st_gid,final.st_dev,final.st_ino,final.st_nlink,final.st_size,final.st_mtime_ns,final.st_ctime_ns), 'object changed during full inventory'
        result[str(current.relative_to(directory))] = value
    assert len(json.dumps(result)) <= 4*LIMIT, 'bounded full tree evidence/hex expansion'
    return result


def json_members(value, path=''):
    result = []
    def visit(node, location, depth):
        assert depth <= 64 and len(result) < 65536, 'full JSON member bound'
        result.append(location)
        if type(node) is dict:
            for key in sorted(node): visit(node[key],location+'/'+key.replace('~','~0').replace('/','~1'),depth+1)
        elif type(node) is list:
            for i,child in enumerate(node): visit(child,location+'/'+str(i),depth+1)
    visit(value,path,0)
    return result


def authoritative_readback(root, primary, selector, binding, phase, mode='live'):
    """Actual public Load/Inspect/Gate/Revalidate; the first token is never recaptured."""
    assert READBACK_INPUTS is not None
    key = str(root)
    frozen = AUTHORITY_BASELINE.get(key)
    request = {'mode':mode,'selector':str(selector),'binding':binding,'tempRoot':str(root/'tmp'),
               'timeoutMillis':max(1,int(CASE_CLOCK.wait(5)*1000)), 'sourceSHA256':READBACK_SOURCE_SHA256,
               'originalAuthority':frozen['originalAuthority'] if frozen else None,
               'originalNamespace':frozen['originalNamespace'] if frozen else '',
               'fixed':frozen['fixed'] if frozen else None}
    observed = strict_json(cli(root,pathlib.Path(READBACK_INPUTS['readbackHelper']),[],phase+'-public-authority',json.dumps(request).encode()))
    assert observed['mode'] == mode and observed['bridgeSourceSHA256'] == READBACK_SOURCE_SHA256
    assert observed['binding'] == binding
    recovery = observed['inspection']['Recovery']
    assert recovery['Required'] is False and not recovery['Reason']
    assert not recovery['Journals'] and not recovery['Receipts'] and not recovery['NativeIntents']
    state_path = root/'home/.config/uap/state/state-v2.json'
    raw = strict_json(state_path.read_bytes())
    assert raw['schema_version'] == 4 and raw.get('installations',[]) == (observed['state'].get('installations') or []), 'actual Load versus raw state membership'
    operations = state_path.parent/'operations'
    if operations.exists():
        assert not list(operations.iterdir()), 'raw operation remainder is unknown'
    if mode != 'absent':
        selected = observed['selected']
        assert selected['target_locator'] == str(root/'home/.cursor/plugins/local'/selector.parent.name), 'selected managed MCP target changed'
        authority = selected['profile_authority']
        assert authority == observed['originalAuthority'] and selected['profile_namespace'] == observed['originalNamespace']
        assert set(authority) == {'version','canonical_root','ancestry'} and authority['version'] == 1
        assert authority['canonical_root'] == binding['scopeRoot'] and authority['ancestry']
        assert all(set(entry) == {'canonical_path','scheme','volume_id','object_id'} for entry in authority['ancestry'])
        assert selected['materialization'] == 'materialized' and selected['activation'] == 'prepared' and selected['verification'] == 'package_validated', 'registration readback remains prepared/package-valid'
        assert selected['policy'] == 'allowed' and not selected.get('pending_native_intent') and not selected.get('native_activation_attempt')
        objects = selected['native_objects']
        assert len(objects) == 2 and sorted(o['kind'] for o in objects) == ['cursor_user_stop','managed_package_directory']
        expected_protection = {'cursor_user_stop':'owned_selector','managed_package_directory':'managed'}
        assert all(not o.get('user_modified',False) and o['protection_class'] == expected_protection[o['kind']] for o in objects)
        if frozen:
            assert authority == frozen['originalAuthority'] and observed['originalNamespace'] == frozen['originalNamespace'], 'original raw complete ordered authority changed'
            assert observed['fixed'] == frozen['fixed']
        else:
            AUTHORITY_BASELINE[key] = {k:copy.deepcopy(observed[k]) for k in ('originalAuthority','originalNamespace','fixed')}
            (root/'evidence/original-profile-authority.json').write_text(json.dumps(AUTHORITY_BASELINE[key],indent=2)+'\n')
        channels = observed['channels']
        expected = phase in ('enabled','native-enabled','duplicate')
        assert channels == {'Desktop':expected,'Webhook':expected}, 'actual effective channel consent readback'
        assert observed['consumerBinding']['Generation'] == strict_json((root/'home/.config/agent-notifications/ownership.json').read_bytes())['Generation']
    (root/'evidence'/(phase+'-authoritative-readback.json')).write_text(json.dumps(observed,indent=2)+'\n')
    return observed


def installed_readback(root, primary, artifact, phase, selector=None):
    control = root/'home/.config/agent-notifications'
    raw = {str(p.relative_to(root)):p.read_bytes() for p in
           [control/'ownership.json', control/'agent-notifications.json',
            control.parent/'uap/state/state-v2.json', root/'home/.cursor/hooks.json']}
    assert not (control/'transaction.json').exists(), 'recovery is not resolved'
    ledger = strict_json(raw[str((control/'ownership.json').relative_to(root))])
    assert ledger['Owner'] == 'existing-installer' and ledger['Generation'] > 0 and not ledger.get('PendingMutation')
    assert ledger['RuntimeRoot'] == str(root/'home/.local')
    assert primary == pathlib.Path(ledger['RuntimeRoot'])/'bin/claude-notifications-linux-amd64'
    identity = ledger['Files'][str(primary)]
    assert identity['Exists'] is True and not identity['Link'] and identity['SHA256'] == digest(primary)
    assert identity['Mode'] == stat.S_IMODE(primary.stat().st_mode)
    hooks = strict_json(raw['home/.cursor/hooks.json'])
    assert hooks['version'] == 1
    entries = []
    for hook in hooks['hooks']['stop']:
        argv = shlex.split(hook['command'])
        if argv[:4] == [str(primary), 'cursor-event', 'stop', '--binding']:
            assert len(argv) == 5 and hook['timeout'] == 5 and hook.get('failClosed') is False
            entries.append(argv)
    assert len(entries) == 1, 'exact USER owned helper hook required'
    actual = physical(entries[0][4])
    binding = strict_json(actual.read_bytes())
    assert selector is None or actual == selector, 'same installed selector required'
    assert binding['integration'] == 'cursor' and binding['scopeRoot'] == str(root/'home/.cursor')
    assert binding['version'] == 1 and binding['owner'] == ledger['Owner'] and binding['componentID'] == ledger['ID']
    assert binding['controlRoot'] == str(control) and binding['runtimeRoot'] == ledger['RuntimeRoot']
    assert binding['primary'] == 'bin/claude-notifications-linux-amd64'
    assert binding['globalConfig'] == case_env(root)['AGENT_NOTIFICATIONS_CONFIG']
    assert physical(binding['dataRoot']) == actual.parent
    # Hash committed; raw has no authority/ACK.
    consumers = [(key, value) for key, value in ledger['Consumers'].items()
                 if key.startswith('portable:') and strict_json(value['Registration']) == binding]
    assert len(consumers) == 1, 'one exact selected registered consumer required'
    key, consumer = consumers[0]
    registration = consumer['Registration'].encode('utf-8')
    assert key == 'portable:'+hashlib.sha256(registration).hexdigest()
    assert actual.name == 'agent-notify-'+key[len('portable:'):]+'.json'
    assert actual.read_bytes() == registration and consumer['RuntimeRoot'] == ledger['RuntimeRoot']
    assert consumer['Commands'] == [str(primary)]
    # Readback authority/ACK/receipt gated.
    report = strict_json(cli(root, primary, wizard(root, primary, artifact, None, 'inspect', True), phase+'-inspect'))
    assert report['action'] == 'inspect' and report['outcome'] == 'completed', 'inspect exit 0 is insufficient'
    assert report['installationID'] == binding['installationID']
    selected = [row for row in report['targets'] if row['client'] == 'cursor' and row['unit'] == 'agent-notify']
    assert len(selected) == 1 and selected[0]['outcome'] == 'installed'
    assert selected[0]['reason'] == binding['bindingID'] and selected[0]['treeDigest']
    assert selected[0]['profile'] == binding['scopeRoot']
    assert not any(row['outcome'] in ('unknown', 'incomplete') for row in report['targets'])
    assert not any(row['kind'] in ('recover', 'resume', 'activate', 'external-uninstall') for row in report.get('nextActions', []))
    authoritative_readback(root,primary,actual,binding,phase)
    saved = {name:base64.b64encode(data).decode() for name, data in raw.items()}
    (root/'evidence'/str(phase+'-readback.json')).write_text(json.dumps(saved, indent=2)+'\n')
    return actual, binding, ledger['Generation']


def install_and_confirm(root, artifact, inputs, webhook_url):
    stage = pathlib.Path(inputs['stage'])
    control = root/'home/.config/agent-notifications'
    # Pinned native file-reader fixture, retained in the initial foreign HOME inventory.
    home = root/'home'; home_facts = home.lstat()
    if not stat.S_ISDIR(home_facts.st_mode) or stat.S_IMODE(home_facts.st_mode) != 0o700 or (home_facts.st_uid,home_facts.st_gid) != (1000,1000):
        raise ValueError('TEST credential HOME identity')
    config = home/'.config'; config.mkdir(mode=0o700)
    credentials = config/'cursor'; credentials.mkdir(mode=0o700)
    directory_facts = credentials.lstat()
    if not stat.S_ISDIR(directory_facts.st_mode) or stat.S_IMODE(directory_facts.st_mode) != 0o700 or (directory_facts.st_uid,directory_facts.st_gid,directory_facts.st_dev) != (1000,1000,home_facts.st_dev):
        raise ValueError('TEST credential directory identity')
    credential_path = credentials/'auth.json'
    synthetic_token = 'TEST-loopback-not-a-secret'
    with os.fdopen(os.open(credential_path,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600),'w') as credential_file:
        file_facts = os.fstat(credential_file.fileno())
        if not stat.S_ISREG(file_facts.st_mode) or stat.S_IMODE(file_facts.st_mode) != 0o600 or file_facts.st_nlink != 1 or (file_facts.st_uid,file_facts.st_gid,file_facts.st_dev) != (1000,1000,home_facts.st_dev):
            raise ValueError('TEST credential file identity')
        credential_file.write(json.dumps({'accessToken':synthetic_token,'refreshToken':synthetic_token},indent=2))
    final_facts = credential_path.lstat()
    if (final_facts.st_mode,final_facts.st_uid,final_facts.st_gid,final_facts.st_dev,final_facts.st_ino,final_facts.st_nlink) != (file_facts.st_mode,file_facts.st_uid,file_facts.st_gid,file_facts.st_dev,file_facts.st_ino,1):
        raise ValueError('TEST credential file replaced')
    (root/'evidence/credential-fixture.json').write_text(json.dumps({'kind':'TEST-synthetic-file-credentials',
        'relativePath':'.config/cursor/auth.json','directoryMode':0o700,'fileMode':0o600,'uid':1000,'gid':1000,
        'fields':['accessToken','refreshToken'],'beforeForeignBaseline':True},separators=(',',':'))+'\n')
    target = root/'home/.local/bin'
    target.mkdir(mode=0o700, parents=True)
    primary = stage/'claude-notifications-linux-amd64'
    cli(root, primary, ['internal-install-runtime', '--stage', str(stage), '--target', str(target),
        '--entry', primary.name, '--control-root', str(control)], 'core-register')
    ledger = strict_json((control/'ownership.json').read_bytes())
    assert ledger['RuntimeRoot'] == str(target.parent), 'core RuntimeRoot is parent of stable bin target'
    primary = target/primary.name
    assert digest(primary) == inputs['sha256']['primary'], 'committed core helper bytes'
    cli(root, primary, ['config', 'init', '--json'], 'config-init')
    inspection = strict_json(cli(root, primary, ['config', 'inspect', '--json'], 'config-inspect'))
    assert inspection['valid'] is True and inspection['revision']
    edits = {'set':{'/notifications/desktop/enabled':True, '/notifications/webhook/enabled':True,
        '/notifications/webhook/url':webhook_url, '/notifications/webhook/preset':'custom', '/notifications/webhook/format':'json',
        '/statuses/agent_stopping/enabled':True, '/statuses/agent_stopping/desktop/enabled':True,
        '/statuses/agent_stopping/webhook/enabled':True}}
    cli(root, primary, ['config', 'edit', '--stdin', '--expect-revision', inspection['revision']],
        'config-edit', json.dumps(edits).encode())
    with os.fdopen(os.open(control/'agent-notifications.json', os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600), 'wb') as policy: policy.write(b'{"schemaVersion":1,"enabled":false}\n')
    assert strict_json((control/'ownership.json').read_bytes()) == ledger, 'TEST policy initialization must not change core ledger'
    (root/'evidence/policy-init.json').write_text(json.dumps({'path':str(control/'agent-notifications.json'), 'policy':strict_json((control/'agent-notifications.json').read_bytes()), 'mode':(control/'agent-notifications.json').stat().st_mode, 'generation':ledger['Generation']})+'\n')
    foreign = foreign_preimage(root)
    installed_report = strict_json(cli(root, primary, wizard(root, primary, artifact, inputs['package'], 'install', True), 'install'))
    assert installed_report['action'] == 'install' and installed_report['outcome'] == 'completed', 'cancelled exit 0 is insufficient'
    selector, binding, generation = installed_readback(root, primary, artifact, 'install')
    assert installed_report['installationID'] == binding['installationID'] and installed_report['generation'] == generation
    selected = [row for row in installed_report['targets'] if row['client'] == 'cursor' and row['unit'] == 'agent-notify']
    assert len(selected) == 1 and selected[0]['outcome'] == 'completed' and selected[0]['reason'] == binding['bindingID']
    intent = primary.parent/'TEST-cursor-confirmed-intent.json'
    assert not intent.exists()
    channel_args = ['--products', 'cursor', '--agent-notify', '--desktop', '--webhook']
    cli(root, primary, ['setup-products', 'confirm', '--plain', *channel_args, '--scope-root', str(root/'home/.cursor'),
        '--client-executable', str(artifact/'cursor-agent'), '--control-root', str(control),
        '--intent-file', str(intent)], 'confirm', terminal=True)
    cli(root, primary, ['setup-products', 'intent-args', '--intent-file', str(intent)], 'intent-args')
    cli(root, primary, ['setup-products', 'preflight', '--intent-file', str(intent), *channel_args], 'preflight')
    frozen = strict_json(intent.read_bytes())
    assert frozen['provenance']['SourceCommit'] == SOURCE_SHA and frozen['provenance']['SHA256'] == digest(primary)
    scopes = {name:base64.b64decode(value, validate=True).decode() for name, value in frozen['scopes'].items()}
    assert scopes['scope-root'] == str(root/'home/.cursor') and scopes['client-executable'] == str(artifact/'cursor-agent')
    args = ['setup-notifications', 'wizard', '--action', 'install', '--install-or-update', '--agents', 'cursor',
            '--hooks', 'false', '--agent-notify', 'true', '--yes', '--package', inputs['package'],
            '--bootstrap-intent-file', str(intent)]
    for key in ['control-root', 'runtime-root', 'global-config', 'scope-root', 'client-executable']:
        args += ['--'+key, scopes[key]]
    # Update before consent CAS.
    cli(root, primary, args, 'update-before-consent')
    selector, binding, generation = installed_readback(root, primary, artifact, 'enabled', selector)
    foreign['ownedOperatorObjects'] = {str(intent.relative_to(root/'home')):bounded_tree(intent)['.']}
    return (primary, selector, binding, generation), foreign


def webhook_source_body(payload):
    value = strict_json(payload)
    expected = {'schema_version':'1.0','notification_type':'agent_stopping','status':'agent_stopping',
                'agent_source':'cursor','message':'Cursor CLI is stopping','title':'Cursor CLI',
                'session_id':'','source':'claude-notifications'}
    assert type(value) is dict and set(value) == set(expected)|{'timestamp'}, 'unknown webhook source fields'
    assert all(value[key] == wanted for key,wanted in expected.items()), 'wrong fixed Cursor stopping webhook copy'
    assert type(value['timestamp']) is str and re.fullmatch(r'\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2})',value['timestamp']), 'unknown source RFC3339 timestamp'
    if value['timestamp'][-1] != 'Z':
        assert int(value['timestamp'][-5:-3]) <= 23 and int(value['timestamp'][-2:]) <= 59, 'invalid RFC3339 numeric offset'
    datetime.datetime.fromisoformat(value['timestamp'])
    return value


def owned_webhook(root):
    records = []
    lock = threading.Lock()
    class Webhook(http.server.BaseHTTPRequestHandler):
        def setup(self):
            super().setup()
            self.connection.settimeout(CASE_CLOCK.wait(1))
        def do_GET(self):
            raise AssertionError('unsupported webhook method')
        def log_message(self, *args): pass
        def do_POST(self):
            assert self.path == '/TEST-cursor-stop' and self.client_address[0] == '127.0.0.1'
            assert self.headers.get('Content-Type', '').startswith('application/json')
            size = int(self.headers.get('Content-Length', '-1'))
            assert 0 <= size <= 65536
            payload = self.rfile.read(size)
            assert len(payload) == size
            with lock:
                assert len(records) < 2, 'no repeated channel effects allowed'
                record = {'bodyHex':payload.hex(), 'json':webhook_source_body(payload), 'responseAccepted':False}
                records.append(record)
                (root/'evidence/webhook-actual.json').write_text(json.dumps(records, indent=2)+'\n')
            self.send_response(200)
            self.send_header('Content-Length', '0')
            self.end_headers()
            self.wfile.flush()
            with lock:
                record['responseAccepted'] = True
                (root/'evidence/webhook-actual.json').write_text(json.dumps(records,indent=2)+'\n')
    class OwnedServer(http.server.HTTPServer):
        def handle_error(self, request, address):
            assert len(self.case_failures) < 2, 'webhook failure budget'
            self.case_failures.append(type(sys.exc_info()[1]).__name__)
            (root/'evidence/webhook-failures.json').write_text(json.dumps(self.case_failures)+'\n')
    server = OwnedServer(('127.0.0.1', 0), Webhook)
    server.case_failures = []
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, thread, records, 'http://127.0.0.1:'+str(server.server_port)+'/TEST-cursor-stop'


def foreign_preimage(root):
    profile = root/'home/.cursor'
    control = root/'home/.config/agent-notifications'
    ledger = strict_json((control/'ownership.json').read_bytes())
    config = pathlib.Path(case_env(root)['AGENT_NOTIFICATIONS_CONFIG'])
    state_path = control.parent/'uap/state/state-v2.json'
    return {'home':bounded_tree(root/'home'),'profile':bounded_tree(profile), 'cache':bounded_tree(root/'cache'),
            'globalConfig':bounded_tree(config),'otherConsumers':ledger['Consumers'],
            'uapState':strict_json(state_path.read_bytes()) if state_path.exists() else {'schema_version':4,'installations':[]},
            'policy':strict_json((control/'agent-notifications.json').read_bytes())}


def same_preserved_object(before, after, directory_members_changed=False):
    if before == after: return True
    if directory_members_changed and stat.S_ISDIR(before['mode']) and stat.S_ISDIR(after['mode']):
        return {k:v for k,v in before.items() if k != 'nlink'} == {k:v for k,v in after.items() if k != 'nlink'}
    return False


def installation_map(state):
    rows = state.get('installations') or []
    assert type(rows) is list and len(rows) <= 1024, 'installation membership bound'
    result = {}
    for row in rows:
        key = row['installation_id']
        assert type(key) is str and key and key not in result, 'duplicate/unknown installation identity'
        result[key] = row
    return result


def removal_memberships(before, after, binding, foreign_state):
    """Exact foreign membership; only source-defined selected removal changes."""
    old,new,foreign = installation_map(before),installation_map(after),installation_map(foreign_state)
    assert list(old) == list(new), 'ordered installation addition/deletion during removal'
    selected_id = binding['installationID']
    assert selected_id in old and selected_id not in foreign, 'selected installation preimage'
    for key in old:
        if key != selected_id: assert old[key] == new[key], 'foreign installation/client/dataReceipt changed'
    for key in foreign: assert key in old and old[key] == new[key] == foreign[key], 'original foreign installation membership changed'
    left,right = copy.deepcopy(old[selected_id]),copy.deepcopy(new[selected_id])
    clients = left['clients']
    selected = [(key,value) for key,value in clients.items() if value['client_binding_id'] == binding['bindingID']]
    assert len(selected) == 1 and len(clients) == 1, 'SAME one selected client removal'
    client_key,client = selected[0]
    assert right['clients'] == {} and right.get('data_retained') is True, 'selected binding/retained data disposition'
    assert left.get('data_receipts') and left.get('data_receipts') == right.get('data_receipts'), 'complete selected acknowledged data receipt membership'
    preferences = copy.deepcopy(left.get('install_preferences',[]))
    assert client.get('install_intent','') in ('','prepare'), 'unknown source install intent'
    if client.get('install_intent','') != '':
        preference = {'client_id':client['client_id'],'scope':client['scope'],'install_intent':client.get('install_intent','')}
        matches = [i for i,row in enumerate(preferences) if row['client_id'] == preference['client_id'] and row['scope'] == preference['scope']]
        assert len(matches) <= 1, 'ambiguous selected install preference'
        if matches: preferences[matches[0]] = preference
        else: preferences.append(preference)
    assert right.get('install_preferences',[]) == preferences, 'foreign/selected preference membership'
    assert type(right.get('operation_group_id')) is str and 0 < len(right['operation_group_id'].encode()) <= 256, 'actual committed removal operation group'
    assert type(right['updated_at']) is str and right['updated_at'] >= left['updated_at'], 'actual removal time'
    for field in ('clients','data_retained','updated_at','operation_group_id','install_preferences'):
        left.pop(field,None);right.pop(field,None)
    assert left == right, 'unknown selected installation mutation'
    top_before = {key:value for key,value in before.items() if key != 'installations'}
    top_after = {key:value for key,value in after.items() if key != 'installations'}
    assert top_before == top_after, 'unknown state top-level/transaction receipt mutation'
    return {'installationIDsBefore':list(old),'installationIDsAfter':list(new),'foreignInstallationsPreserved':{key:new[key] for key in new if key != selected_id},
            'selectedClientRemoved':client,'selectedDataReceiptsRetained':new[selected_id]['data_receipts'],
            'selectedCommittedAfter':new[selected_id],'stateJSONMembersBefore':json_members(before),'stateJSONMembersAfter':json_members(after)}


def classify_home_removal(root,binding,foreign,before,after,native,classified,memberships):
    """Cover every HOME path, including complete control/UAP and foreign sets."""
    home = root/'home'
    mapped = {}
    roots = {'profile':home/'.cursor','data':pathlib.Path(binding['dataRoot']),'projection':None}
    for row in classified:
        base = roots.get(row['area'])
        if base is not None and (base == home or home in base.parents):
            path = str((base/row['path']).relative_to(home))
            assert path not in mapped or mapped[path] == row['disposition'], 'ambiguous full HOME classification'
            mapped[path] = row['disposition']
    control = pathlib.Path(binding['controlRoot'])
    state = control.parent/'uap/state'
    semantic = {str((control/'agent-notifications.json').relative_to(home)):'owned selected disabled channels retained; exact other policy members preserved',
                str((control/'ownership.json').relative_to(home)):'owned ledger exact selected consumer removal and actual generation changes',
                str((state/'state-v2.json').relative_to(home)):'owned state exact selected client removal with complete retained/foreign receipt membership'}
    result = []
    for path in sorted(set(before)|set(after)):
        old,new = before.get(path),after.get(path)
        disposition = mapped.get(path)
        if disposition is None and path in semantic:
            assert old and new and all(old[k] == new[k] for k in ('mode','uid','gid','device','nlink')), 'committed metadata ownership changed'
            assert stat.S_ISREG(new['mode']) and 'link' not in old and 'link' not in new, 'semantic file is not regular'
            disposition = semantic[path]
        if disposition is None and path in foreign['home']:
            assert new is not None and same_preserved_object(foreign['home'][path],new,True), 'foreign full HOME preimage changed'
            disposition = 'foreign full HOME object/member set retained'
        if disposition is None and path in foreign.get('ownedOperatorObjects',{}):
            assert new == old == foreign['ownedOperatorObjects'][path], 'owned TEST intent altered'
            disposition = 'owned retained explicit TEST consent intent'
        if disposition is None and path in native:
            assert old and new and same_preserved_object(native[path],old,True) and same_preserved_object(old,new,True), 'native HOME retained artifact changed/purged'
            disposition = 'owned retained natural Cursor HOME artifact from native boundaries'
        if disposition is None and old and new and stat.S_ISDIR(new['mode']):
            assert same_preserved_object(old,new,True), 'owned structural directory metadata drift'
            children = [key for key in set(before)|set(after) if key != path and key.startswith(path.rstrip('/')+'/')]
            fixed = {'.config/uap','.config/uap/state','.config/uap/state/operations','.config/uap/managed','.config/uap/plugin-data'}
            assert children or path in fixed, 'unknown empty retained HOME directory'
            disposition = 'owned retained structural directory; all concrete child memberships classified'
        if disposition is None and path == '.config/uap/state/mutation.lock':
            assert old == new and new and stat.S_ISREG(new['mode']), 'mutation lock unknown identity/membership'
            disposition = 'owned retained source-standard mutation lock'
        assert disposition is not None, 'unclassified full HOME object addition/deletion/remainder: '+path
        result.append({'area':'home','path':path,'disposition':disposition,'before':old,'after':new})
    return result


def classify_removal(root,binding,selected,foreign,before,after,phases,expected_hook,state,original_state,before_ledger,after_ledger,before_policy,after_policy):
    """Every concrete path has one observed owner/disposition; unknown refuses."""
    target = pathlib.Path(selected['target_locator'])
    profile = root/'home/.cursor'
    projection_prefix = str(target.relative_to(profile)) if profile in target.parents else None
    native = {'home':{},'profile':{},'cache':{}}
    for phase in phases:
        for area in native:
            left,right = phase['nativeObjectsBefore'][area],phase['nativeObjectsAfter'][area]
            for path,facts in right.items():
                if left.get(path) != facts:
                    assert facts['uid'] == facts['gid'] == 1000, 'native retained object credential drift'
                    native[area][path] = facts
    result = []
    for area in ('profile','cache','data','projection'):
        left,right = before[area],after[area]
        for path in sorted(set(left)|set(right)):
            old,new = left.get(path),right.get(path)
            disposition = None
            is_projection = area == 'projection' or area == 'profile' and projection_prefix is not None and (path == projection_prefix or path.startswith(projection_prefix+'/'))
            if is_projection:
                assert old is not None and new is None, 'selected owned projection remainder/addition'
                disposition = 'owned projection removed including MCP/skills/binding membership'
            if disposition is None and area == 'data':
                selector = pathlib.Path(binding['dataRoot'])/path
                if selector == pathlib.Path(AUTHORITY_BASELINE[str(root)]['fixed']['Selector']):
                    assert old is not None and new is None, 'selected owned selector remainder'
                    disposition = 'owned selected selector removed'
            if disposition is None and area == 'profile' and path == 'hooks.json':
                assert old is not None and new is not None and new['sha256'] == hashlib.sha256(expected_hook).hexdigest() and new['byteLength'] == len(expected_hook)
                assert all(old[k] == new[k] for k in ('mode','uid','gid','device','nlink')), 'remaining hooks permissions/ownership'
                disposition = 'owned stop member removed; exact public pure removal remainder retained'
            if disposition is None and area == 'data':
                assert old is not None and new is not None and same_preserved_object(old,new,True), 'PLUGIN_DATA/receipt/cache purge or unknown addition'
                disposition = 'owned retained PLUGIN_DATA/receipt/observation cache'
            if disposition is None and path in foreign.get(area,{}):
                original = foreign[area][path]
                assert new is not None and same_preserved_object(original,new,True), 'foreign preimage object/member/bytes changed'
                disposition = 'foreign preimage retained'
            if disposition is None and path in native.get(area,{}):
                assert old is not None and new is not None and same_preserved_object(old,new,True), 'natural native data removed/changed'
                assert same_preserved_object(native[area][path],old,True), 'native phase receipt does not match pre-remove object'
                disposition = 'owned retained natural Cursor trust/cache/session object from observed native boundary'
            if disposition is None and new is not None and stat.S_ISDIR(new['mode']):
                # ACK/receipt identity/creds.
                known = [p for p in native.get(area,{}) if p.startswith(path+'/')]
                if area == 'profile' and projection_prefix and projection_prefix.startswith(path+'/'): known.append(projection_prefix)
                assert known and old is not None and same_preserved_object(old,new,True), 'unknown retained directory'
                disposition = 'owned retained structural parent with classified child membership'
            assert disposition is not None, 'unclassified removal object: '+area+'/'+path
            result.append({'area':area,'path':path,'disposition':disposition,'before':old,'after':new})
    memberships = removal_memberships(original_state,state,binding,foreign['uapState'])
    ledger_before = copy.deepcopy(before_ledger); ledger_after = copy.deepcopy(after_ledger)
    selected_keys = [key for key,value in before_ledger['Consumers'].items() if strict_json(value['Registration']) == binding]
    assert len(selected_keys) == 1, 'exact pre-remove actual selected consumer'
    selected_key = selected_keys[0]
    expected_consumers = {key:value for key,value in before_ledger['Consumers'].items() if key != selected_key}
    assert after_ledger['Consumers'] == expected_consumers == foreign['otherConsumers'], 'full foreign consumer/binding membership'
    delta = after_ledger['Generation']-before_ledger['Generation']
    assert type(delta) is int and delta > 0 and after_ledger['PolicyGeneration']-before_ledger['PolicyGeneration'] == delta, 'actual removal generations'
    for field in ('Generation','PolicyGeneration','Consumers'):
        ledger_before.pop(field); ledger_after.pop(field)
    assert ledger_before == ledger_after and not after_ledger.get('PendingMutation'), 'unknown ledger mutation/membership'
    assert before_policy == after_policy, 'uninstall may not mutate foreign/disabled policy members'
    foreign_policy = copy.deepcopy(foreign['policy']); retained_policy = copy.deepcopy(after_policy)
    assert retained_policy.get('route',{}).get('cursorNotifications') == {'desktop':False,'webhook':False}, 'removed selected channels must remain disabled'
    for document in (foreign_policy,retained_policy):
        if 'route' in document:
            document['route'].pop('cursorNotifications',None)
            if not document['route']: document.pop('route')
    assert foreign_policy == retained_policy, 'foreign policy JSON members/values changed'
    home_result = classify_home_removal(root,binding,foreign,before['home'],after['home'],native['home'],result,memberships)
    return {'objects':result+home_result,'stateMembership':memberships,
            'consumerMembership':{'before':before_ledger['Consumers'],'after':after_ledger['Consumers'],'selectedRemoved':selected_key},
            'ledgerSemanticChanges':{'Generation':delta,'PolicyGeneration':delta},'policyMembersPreserved':json_members(after_policy),
            'unknownRemainders':[]}



def finish_lifecycle(root, artifact, package, installed, first_phase, observation, clock, records, foreign):
    primary, selector, binding, generation = installed
    native_bytes = first_phase['observation']['nativeBytes']
    assert type(native_bytes) is bytes and 0 < len(native_bytes) <= LIMIT, 'actual native bytes required'
    assert first_phase['result']['status'] == 'PASS'
    assert len(records) == 1 and records[0]['responseAccepted'] is True, 'one actual accepted webhook response'
    baseline = copy.deepcopy(records)
    def check_no_new_effects():
        observation.exchange('checkpoint')
        assert records == baseline, 'duplicate/disabled webhook delivery'
    installed_readback(root, primary, artifact, 'native-enabled', selector)
    cli(root, primary, ['cursor-event', 'stop', '--binding', str(selector)], 'duplicate', native_bytes)
    check_no_new_effects()
    installed_readback(root, primary, artifact, 'duplicate', selector)
    cli(root, primary, wizard(root, primary, artifact, None, 'install', False), 'disable')
    installed_readback(root, primary, artifact, 'disabled', selector)
    policy = strict_json((root/'home/.config/agent-notifications/agent-notifications.json').read_bytes())
    assert policy['route']['cursorNotifications'] == {'desktop':False, 'webhook':False}
    cli(root, primary, ['cursor-event', 'stop', '--binding', str(selector)], 'disabled-stop', native_bytes)
    check_no_new_effects()
    cli(root, primary, wizard(root, primary, artifact, package, 'update', True), 'update')
    installed_readback(root, primary, artifact, 'updated', selector)
    policy = strict_json((root/'home/.config/agent-notifications/agent-notifications.json').read_bytes())
    assert policy['route']['cursorNotifications'] == {'desktop':False, 'webhook':False}
    restarted = native_run(root, artifact, installed, 'disabled-restart', clock, observation)
    assert restarted['result']['status'] == 'PASS', restarted['result']['failure']
    assert restarted['observation']['facts']['conversation'] != first_phase['observation']['facts']['conversation']
    assert restarted['observation']['facts']['generation'] != first_phase['observation']['facts']['generation']
    check_no_new_effects()
    installed_readback(root, primary, artifact, 'restarted', selector)
    installed_readback(root, primary, artifact, 'pre-remove', selector)
    before = authoritative_readback(root,primary,selector,binding,'pre-remove','remove-plan')
    selected = before['selected']
    target = pathlib.Path(selected['target_locator'])
    assert physical(target) == target and target != root and root in target.parents, 'selected actual projection boundary'
    before_trees = {'home':bounded_tree(root/'home'),'profile':bounded_tree(root/'home/.cursor'),'cache':bounded_tree(root/'cache'),
                    'data':bounded_tree(binding['dataRoot']),'projection':bounded_tree(target)}
    before_ledger = strict_json((pathlib.Path(binding['controlRoot'])/'ownership.json').read_bytes())
    before_policy = strict_json((pathlib.Path(binding['controlRoot'])/'agent-notifications.json').read_bytes())
    removal = strict_json(cli(root, primary, wizard(root, primary, artifact, None, 'uninstall', True), 'remove'))
    assert removal['action'] == 'uninstall' and removal['outcome'] == 'completed' and not removal.get('nextActions')
    after_public = authoritative_readback(root,primary,selector,binding,'removed','absent')
    inspect = strict_json(cli(root,primary,wizard(root,primary,artifact,None,'inspect',True),'removed-inspect'))
    selected_rows = [row for row in inspect['targets'] if row['client'] == 'cursor' and row['unit'] == 'agent-notify']
    assert inspect['outcome'] == 'completed' and len(selected_rows) == 1 and selected_rows[0]['outcome'] == 'absent'
    assert not inspect.get('nextActions') and not any(row['outcome'] in ('unknown','incomplete','installed') for row in inspect['targets'])
    after = foreign_preimage(root)
    assert after['globalConfig'] == foreign['globalConfig'], 'full foreign global config bytes/mode/UIDGID/raw-link identity'
    assert after['otherConsumers'] == foreign['otherConsumers'], 'other actual core ledger bindings preserved'
    assert not selector.exists() and not target.exists(), 'selected consumer/selector/projection/MCP/skills removal'
    assert not (root/'home/.config/agent-notifications/transaction.json').exists()
    after_trees = {'home':after['home'],'profile':after['profile'],'cache':after['cache'],'data':bounded_tree(binding['dataRoot']),
                   'projection':bounded_tree(target)}
    after_ledger = strict_json((pathlib.Path(binding['controlRoot'])/'ownership.json').read_bytes())
    after_policy = strict_json((pathlib.Path(binding['controlRoot'])/'agent-notifications.json').read_bytes())
    expected_hook = base64.b64decode(before['expectedHookBytes'],validate=True)
    assert hashlib.sha256(expected_hook).hexdigest() == before['expectedHookSHA256']
    assert (root/'home/.cursor/hooks.json').read_bytes() == expected_hook, 'actual pure Plan(Remove) remaining bytes'
    classification = classify_removal(root,binding,selected,foreign,before_trees,after_trees,
        [first_phase,restarted],expected_hook,after_public['state'],before['state'],before_ledger,after_ledger,before_policy,after_policy)
    classified_bytes = (json.dumps(classification,indent=2)+'\n').encode()
    assert len(classified_bytes) <= 16*LIMIT, 'full removal evidence file bound'
    (root/'evidence/removal-full-membership.json').write_bytes(classified_bytes)
    check_no_new_effects()
    return restarted


def validate_installed_effects(root, run, installed, observation, phase, leader, clock, cleanup_signal, cleanup_roles):
    reply = observation.exchange('phase',phase=phase,localLeader=leader,cleanupUsed=clock.cleanup_used,cleanupSignal=cleanup_signal,cleanupRoles=cleanup_roles)
    raw = base64.b64decode(reply['nativeBytes'],validate=True)
    assert hashlib.sha256(raw).hexdigest() == reply['nativeSHA256'], 'actual stream bytes/hash transport'
    facts = captured_stop_bytes(raw)
    assert strict_json(raw).get('cursor_version') == '2026.09.28-64d2043', 'actual source-pinned native version'
    assert facts == reply['facts'] and facts['conversation'] == run['conversation'] and facts['generation'] == run['generation'], 'provider correlation is not byte generation'
    assert facts['status'] == 'completed' and facts['loopCountKnown'] and facts['loopCount'] == 0, 'pinned native completed Stop body'
    path = root/'evidence'/phase/'native-consumed-stdin.json'
    path.write_bytes(raw)
    path.with_suffix('.sha256').write_text(reply['nativeSHA256']+'\n')
    return {'nativeBytes':raw,'facts':facts,'identity':reply['helperIdentity']}


def native_cwd_allowances(snapshot, root, installed):
    # R189/R14 UID/ns/PID+birth/adoption.
    primary,selector,binding,generation = installed
    target = root/'home/.cursor/plugins/local'/selector.parent.name
    mcp_argv = [str(target/'bin/claude-notifications'),'portable-launch','--locator',selector.name]
    primary_argv = [str(primary),'portable-primary','--locator',selector.name,'--runtime-sha256',AUTHORITY_BASELINE[str(root)]['fixed']['ExecutableDigest'].removeprefix('sha256:')]
    expected = [str(primary),'cursor-event','stop','--binding',str(selector)]
    document = strict_json((root/'home/.cursor/hooks.json').read_bytes())
    entries = [h['command'] for h in document['hooks']['stop'] if shlex.split(h['command']) == expected]
    assert len(entries) == 1 and binding['scopeRoot'] == str(root/'home/.cursor')
    command = entries[0]; allowed = {}
    for row in snapshot['rows']:
        if row.get('cwd') == str(target) and row.get('exe') == mcp_argv[0] and row.get('argv') == mcp_argv:
            p = row['stat']; allowed[(p[0],p[5])] = str(target)
            continue
        if row.get('cwd') != binding['scopeRoot']: continue
        argv = row.get('argv',[])
        fixed_helper = argv == expected
        fixed_shell = (len(argv) == 7 and row.get('exe') == argv[0]
                       and argv[0] in ('/usr/bin/bash','/bin/bash') and argv[1:4] == ['-O','extglob','-c']
                       and argv[5] == '--' and argv[6] == command
                       and hashlib.sha256(argv[4].encode()).hexdigest() ==
                       'f4c3183f20f16337ef8df116657d288385fd88ca5551c69126c63d26969dbc29')
        assert fixed_helper or fixed_shell, 'unqualified native USER cwd/argv'
        p = row['stat']; allowed[(p[0],p[5])] = binding['scopeRoot']
    for row in snapshot['rows']:
        if row.get('cwd') != binding['runtimeRoot'] or row.get('exe') != str(primary) or row.get('argv') != primary_argv: continue
        p = row['stat']; parents = [r['stat'] for r in snapshot['rows'] if r['stat'][0] == p[1]]
        assert len(parents) == 1 and allowed.get((p[1],parents[0][5])) == str(target), 'unqualified portable primary parent'
        allowed[(p[0],p[5])] = binding['runtimeRoot']
    return allowed


def native_run(root, artifact, installed, phase, clock, observation):
    global w
    native_observation = None
    assert phase in ('enabled-native','disabled-restart')
    observation.exchange('begin-phase',phase=phase)
    assert hashlib.sha256(WIRE_BYTES).hexdigest() == WIRE_SHA
    w = types.ModuleType("accepted_wire")
    exec(compile(WIRE_BYTES, "R14/wire_plan.py", "exec"), w.__dict__)
    evidence = root/'evidence'/phase; evidence.mkdir(mode=0o700)
    condition = threading.Condition(); messages = {}; rows = []
    state = {'seq':0,'run':None,'stopResult':False,'close':False,'sent':False,'terminal':False,
             'sse':False,'failed':None,'requests':0,'active':0}
    handlers = {}; all_handlers = set(); serve_thread = None
    artifact_pins = list(ARTIFACT_PINS)
    artifact_pins += [{'file':'cursor-agent','sha256':LAUNCHER_SHA}]
    for pin in artifact_pins: assert hashlib.sha256((artifact/pin['file']).read_bytes()).hexdigest() == pin['sha256']
    node_hash = hashlib.sha256((artifact/'node').read_bytes()).hexdigest()
    def auth_refusal_metadata(handler):
        path = handler.path
        unary_operation = None
        if path == '/agent.v1.AgentService/RunSSE': category = 'run-sse'
        elif path == '/aiserver.v1.BidiService/BidiAppend': category = 'bidi-append'
        elif path in w.unary_responses(): category = 'known-unary'; unary_operation = path.rsplit('/',1)[1]
        elif path in REFUSALS: category = 'known-refused'
        elif path == '/aiserver.v1.AnalyticsService/BootstrapStatsig': category = 'analytics-bootstrap'
        else: category = 'other'
        authorization = handler.headers.get('Authorization')
        parts = authorization[:7].split(None,1) if authorization is not None else []
        scheme = parts[0].lower() if parts else ''
        return {'method':'POST','pathCategory':category,'unaryOperation':unary_operation,'authPresent':authorization is not None,
                'schemeCategory':('absent' if authorization is None else scheme if scheme in ('bearer','basic') else 'other'),
                'authLength':min(len(authorization),4096) if authorization is not None else 0}
    def fail(e, auth_handler=None):
        with condition:
            first = state['failed'] is None
            state['failed'] = state['failed'] or type(e).__name__ + ':' + str(e)[:240]; condition.notify_all()
        if first:
            try:
                print('native-callback-first-failure',type(e).__name__[:64],source_failure_frames(e,native_run.__code__),file=sys.stderr,flush=True)
            except Exception:
                pass
            if auth_handler is not None:
                try:
                    print('native-auth-refusal',auth_refusal_metadata(auth_handler),file=sys.stderr,flush=True)
                except Exception:
                    pass
    def capture(q):
        with condition:
            rows.append(q); assert len(json.dumps(rows)) <= LIMIT
            (evidence/'protocol.json').write_text(json.dumps(rows,indent=2))
    def consume(rid,seq,logical):
        with condition:
            assert seq >= state['seq'] and seq not in messages, 'duplicate/old sequence'
            messages[seq] = (rid,logical); assert len(messages) <= 16, 'pending append budget'
            while state['seq'] in messages:
                request,q = messages.pop(state['seq']); state['seq'] += 1
                if state['run'] is not None: assert request == state['run']['requestId'], 'request identity'
                capture({'seq':state['seq']-1,'requestId':request,**q})
                if q['kind'] == 'run':
                    assert state['run'] is None; state['run'] = {'requestId':request,**q}
                elif q['kind'] == 'stop-result':
                    assert state['sent'] and not state['stopResult'] and not state['close']; state['stopResult'] = True
                elif q['kind'] == 'exec-close':
                    assert state['stopResult'] and not state['close']; state['close'] = True
                else: assert q['kind'] == 'heartbeat'
            condition.notify_all()
    class H(http.server.BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.1'
        def setup(self):
            super().setup(); self.connection.settimeout(clock.wait(35))
            with condition:
                handlers[threading.current_thread()] = self.connection
                all_handlers.add(threading.current_thread())
                if len(handlers) > 72 or len(all_handlers) > 128: fail(RuntimeError('HTTP connection budget'))
        def finish(self):
            try: super().finish()
            finally:
                with condition: handlers.pop(threading.current_thread(),None); condition.notify_all()
        def log_message(self,*a): pass
        def send_error(self,*a,**kw):
            fail(ValueError('HTTP parse/method error')); super().send_error(*a,**kw)
        def answer(self,status,data,typ='application/proto'):
            self.send_response(status); self.send_header('Content-Type',typ); self.send_header('Content-Length',str(len(data)))
            self.end_headers(); self.wfile.write(data); self.wfile.flush()
        def chunk(self,data):
            assert len(data) <= LIMIT+5, 'frame budget'
            self.wfile.write(hex(len(data))[2:].encode()+b'\r\n'+data+b'\r\n'); self.wfile.flush()
        def await_state(self,predicate):
            with condition:
                assert condition.wait_for(lambda:predicate() or state['failed'],clock.wait(25)), 'native acknowledgement timeout'
                assert not state['failed'], state['failed']
        def do_POST(self):
            with condition: state['active'] += 1
            auth_check = False
            try:
                with condition: state['requests'] += 1; assert state['requests'] <= 128
                auth_check = True
                assert self.headers.get('Authorization') == ('Bearer ' + 'TEST-loopback-not-a-secret')
                auth_check = False
                assert self.headers.get('Content-Encoding','identity') in ['identity','gzip']
                assert self.headers.get('Transfer-Encoding','').lower() in ['', 'chunked']
                data = body(self); capture({'path':self.path,'bodyHex':data.hex(),'syntheticAuthMatches':True})
                if self.path in w.unary_responses(): self.answer(200,w.unary_responses()[self.path]); return
                if self.path == '/aiserver.v1.BidiService/BidiAppend':
                    rid,seq,msg = append(data); logical = classify(msg)
                    self.answer(200,b''); consume(rid,seq,logical); return
                if self.path == '/agent.v1.AgentService/RunSSE':
                    with condition: assert not state['sse']; state['sse'] = True
                    assert len(data) >= 5 and data[0] in [0,1] and struct.unpack('>I',data[1:5])[0] == len(data)-5
                    payload = data[5:]
                    if data[0] == 1:
                        with gzip.GzipFile(fileobj=io.BytesIO(payload)) as f: payload = f.read(LIMIT+1)
                    assert len(payload) <= LIMIT; rid = w.first(payload,1).decode()
                    self.send_response(200); self.send_header('Content-Type','application/connect+proto')
                    self.send_header('Transfer-Encoding','chunked'); self.end_headers()
                    self.await_state(lambda:state['run'] is not None); run = state['run']; assert run['requestId'] == rid
                    with condition: state['sent'] = True
                    self.chunk(w.stop_frame(run['conversation'],run['generation']))
                    capture({'stopSent':True,'conversation':run['conversation'],'generation':run['generation']})
                    self.await_state(lambda:state['stopResult'] and state['close'])
                    self.chunk(w.text_frame()); self.chunk(w.terminal_frames()); self.wfile.write(b'0\r\n\r\n'); self.wfile.flush()
                    with condition: state['terminal'] = True
                    capture({'terminalSent':True}); return
                capture({'unsupportedRPC':self.path})
                self.answer(501,b'{"code":"unimplemented","message":"TEST finite allowlist"}','application/json')
                assert self.path in REFUSALS, 'unknown server effects'
            except Exception as e: fail(e,self if auth_check else None)
            finally:
                with condition: state['active'] -= 1; condition.notify_all()
        def do_GET(self):
            fail(ValueError('unsupported HTTP method'))
            self.answer(501,b'{"code":"unimplemented","message":"TEST finite allowlist"}','application/json')
    class Server(http.server.ThreadingHTTPServer):
        daemon_threads = True
        def process_request(self,request,address):
            t = threading.Thread(target=self.process_request_thread,args=(request,address),daemon=True)
            with condition:
                all_handlers.add(t); handlers[t] = request
                if len(handlers) > 72 or len(all_handlers) > 128: fail(RuntimeError('HTTP connection budget'))
            t.start()  # register before scheduling; shutdown then joins every created callback thread
        def process_request_thread(self,request,address):
            try:
                with condition:
                    if state['failed'] is not None: raise AssertionError(state['failed'])
                tid = threading.get_native_id(); local = trace_task_identity(tid)
                if local['pid'] != tid or local['tgid'] != os.getpid(): raise AssertionError('owned HTTP local thread identity')
                held = {'tid':tid,'birth':local['birth']}
                reply = observation.exchange('controller-thread',phase=phase,thread=held)
                if reply.get('controllerThread') != {**held,'phase':phase}: raise AssertionError('owned HTTP thread ACK identity')
                with condition:
                    if state['failed'] is not None: raise AssertionError(state['failed'])
            except Exception as error:
                fail(error)
                try: self.shutdown_request(request)
                finally:
                    with condition: handlers.pop(threading.current_thread(),None); condition.notify_all()
                return
            super().process_request_thread(request,address)
        def handle_error(self,*a):
            error = sys.exc_info()[1]
            fail(error if error is not None else RuntimeError('HTTP handler error'))
    server = Server(('127.0.0.1',0),H)
    serve_thread = threading.Thread(target=server.serve_forever,daemon=True)
    env = {'HOME':str(root/'home'),'USER':'TEST','LOGNAME':'TEST','PATH':'/usr/bin:/bin','LANG':'C.UTF-8',
           'TMPDIR':str(root/'tmp'),'XDG_CACHE_HOME':str(root/'cache'),'AGENT_CLI_CREDENTIAL_STORE':'file',
           'CURSOR_AUTH_TOKEN':'TEST-loopback-not-a-secret', **SESSION_ENV}
    argv = [str(artifact/'cursor-agent'),'--endpoint','http://127.0.0.1:'+str(server.server_port),
            '--workspace',str(root/'workspace'),'--model','TEST-native-stop',
            '--trust','--print','--output-format','json','TEST deterministic stop mechanics']
    (evidence/'command.json').write_text(json.dumps({'argv':argv,'cwd':str(root/'workspace'),'envKeys':list(env),
        'network':'new PID+net namespace loopback only','realCredentials':False,'testConsent':CONSENT},indent=2))
    native_before = {'home':bounded_tree(root/'home'),'profile':bounded_tree(root/'home/.cursor'),'cache':bounded_tree(root/'cache')}
    start = time.monotonic(); clock.native_started(); runtime_deadline = clock.work
    birth_floor = int(time.clock_gettime(time.CLOCK_BOOTTIME)*os.sysconf('SC_CLK_TCK'))
    failure = None; clean = False; cleanup_signal = False; natural = False
    namespace = os.readlink('/proc/self/ns/pid'); network = os.readlink('/proc/self/ns/net'); leader = None; roles = []; snapshots = []; cleanup_start = None
    native_seconds = None; cleanup_seconds = None; active_previous = {}; active_checks = 0
    native = subprocess.Popen(argv,cwd=root/'workspace',env=env,stdin=subprocess.DEVNULL,
                              stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
    local_leader = proc_identity(native.pid)
    observation.exchange('leader',phase=phase,localLeader=local_leader,nativeStart=start)
    serve_thread.start()  # Bind before responses.
    def observe_active(phase):
        return capture_active(evidence,own_processes(),namespace,phase,active_previous,active_checks,birth_floor)
    def writable_budget():
        assert sum(p.stat().st_size for p in root.rglob('*') if p.is_file()) <= 1024**3, 'writable bytes budget'
    def drain(stream, name):
        size = 0
        try:
            with (evidence/name).open('xb') as f:
                while True:
                    b = stream.read1(8192)
                    if not b: break
                    size += len(b); assert size <= 131072, 'native output budget'; f.write(b)
        except Exception as e: fail(e)
        finally: stream.close()
    pumps = [threading.Thread(target=drain,args=(pipe,name),daemon=True)
             for pipe,name in [(native.stdout,'native.stdout'),(native.stderr,'native.stderr')]]
    for t in pumps: t.start()
    def check_effects():
        nonlocal native_observation
        assert native.returncode == 0 and not state['failed'], 'native exit or protocol failure'
        assert state['run'] and state['sent'] and state['stopResult'] and state['close'] and state['terminal']
        assert state['active'] == 0 and not messages, 'HTTP callback/pending append join'
        if clean:
            native_observation = validate_installed_effects(root,state['run'],installed,observation,phase,local_leader,clock,cleanup_signal,roles)
        assert not any(q['unsupportedRPC'] not in REFUSALS for q in rows if 'unsupportedRPC' in q)
    try:
        observe_active('active')
        leader = next(p for p in own_processes() if p[0] == native.pid)
        assert leader[0] == leader[2] == leader[4], 'native group/session not unique'
        while native.poll() is None:
            snapshot, active_previous = observe_owned_active(evidence,leader,namespace,network,root,active_previous,
                active_checks,birth_floor,runtime_deadline,native,installed)
            assert not state['failed'], state['failed']
            active_checks += 1; writable_budget(); time.sleep(clock.wait(.1))
        native_seconds = time.monotonic()-start
        assert native_seconds <= 90, 'native runtime budget'; natural = native.returncode == 0
        cleanup_start = time.monotonic(); deadline = clock.cleanup_deadline()
        with condition: assert condition.wait_for(lambda:state['active'] == 0 or state['failed'],min(clock.wait(1,cleanup=True),max(0,deadline-time.monotonic())))
        for t in pumps: t.join(timeout=min(clock.wait(1,cleanup=True),max(0,deadline-time.monotonic())))
        assert all(not t.is_alive() for t in pumps), 'native output pump join'
        check_effects(); validate_output(evidence,state['run'])
        observe_cleanup_facts(evidence,namespace,network,root,active_previous,active_checks,birth_floor,deadline)
        # 8s kernel retry.
        procs = own_processes(); snapshots.append({'phase':'before','processes':procs})
        roles = worker_roles(owned_survivors(procs,leader,1,namespace),artifact,root,namespace)
        writable_budget()
        # Observe first.
        cleanup_signal = request_owned_cleanup(own_processes(),leader,1,namespace,deadline)
        clean = join_owned(native,leader,1,namespace,deadline,budget=writable_budget)
        cleanup_seconds = time.monotonic()-cleanup_start
        assert clean, 'owned descendant join timeout'
        snapshots.append({'phase':'after','processes':own_processes()})
        check_effects(); validate_output(evidence,state['run']); writable_budget()
        for pin in artifact_pins: assert hashlib.sha256((artifact/pin['file']).read_bytes()).hexdigest() == pin['sha256']
        assert hashlib.sha256((artifact/'node').read_bytes()).hexdigest() == node_hash, 'vendor node changed'
    except Exception as e:
        failure = type(e).__name__+':'+str(e)[:240]
        try:
            print('native-observation-failure',type(e).__name__[:64],
                  'outerRemaining',round(clock.outer-time.monotonic(),6),
                  source_failure_frames(e,native_run.__code__),file=sys.stderr,flush=True)
        except Exception:
            pass
    finally:
        # Fail teardown.
        if serve_thread.ident is not None: server.shutdown()
        server.server_close()
        if cleanup_start is None: cleanup_start = time.monotonic(); deadline = clock.cleanup_deadline()
        if serve_thread.ident is not None: serve_thread.join(timeout=min(clock.wait(1,cleanup=True),max(0,deadline-time.monotonic())))
        with condition: pending_handlers = list(handlers.items())
        for thread,conn in pending_handlers:
            try: conn.shutdown(socket.SHUT_RDWR); conn.close()
            except OSError: pass
        http_deadline = min(deadline,time.monotonic()+clock.wait(1,cleanup=True))
        for thread in all_handlers: thread.join(timeout=max(0,http_deadline-time.monotonic()))
        failure = failure or state['failed']
        if handlers or any(t.is_alive() for t in all_handlers) or serve_thread.is_alive() or state['active'] or messages or any(t.is_alive() for t in pumps):
            failure = failure or 'output/HTTP join incomplete'
        (evidence/'cleanup-process-snapshot.json').write_text(json.dumps({'nativePid':native.pid,'controllerPid':1,
            'leaderIdentity':leader,'pidNamespace':namespace,'roles':roles,'snapshots':snapshots},indent=2))
        clock.charge_cleanup(cleanup_start)
    result = {'status':'PASS' if failure is None and clean else 'FAIL','failure':failure,
              'nativeExit':native.returncode,'parentNaturalExit':natural,
              'ownedGroupSIGTERMRequested':cleanup_signal,'ownedNamespaceDescendantsJoined':clean,
              'actualNativeRun':state['run'],'nativeStopAck':state['stopResult'],
              'nativeExecClose':state['close'],'terminalSent':state['terminal'],
              'entry':'installed-USER','GUIVisibility':'NOT_RUN'}
    (evidence/'result.json').write_text(json.dumps(result,indent=2)+'\n')
    native_after = {'home':bounded_tree(root/'home',vendor_root=root),'profile':bounded_tree(root/'home/.cursor',vendor_root=root),'cache':bounded_tree(root/'cache')}
    boundary_bytes = (json.dumps({'before':native_before,'after':native_after},indent=2)+'\n').encode()
    assert len(boundary_bytes) <= 16*LIMIT, 'full native boundary evidence file bound'
    (evidence/'native-retained-object-boundaries.json').write_bytes(boundary_bytes)
    return {'result':result,'observation':native_observation,'nativeObjectsBefore':native_before,'nativeObjectsAfter':native_after}

def main(args):
    global CASE_CLOCK, SESSION_ENV, READBACK_INPUTS
    if args.validate:
        compile(HERE.joinpath('native_case.py').read_bytes(), str(HERE/'native_case.py'), 'exec')
        compile(WIRE_BYTES, 'R14/wire_plan.py', 'exec')
        return 0
    assert args.execute and args.inputs, 'select pure --validate or root --execute with real inputs'
    assert args.consent == CONSENT, 'root ONE-case authorization required'
    CASE_CLOCK = CaseClock(args.outer_deadline if args.inside else time.monotonic()+110)
    inputs = execution_inputs(args.inputs, args.inside)
    READBACK_INPUTS = inputs
    require_native_observation_contract(inputs,CASE_CLOCK,args.inside)
    root, artifact = pathlib.Path(inputs['root']), pathlib.Path(inputs['artifact'])
    if not args.inside:
        assert os.geteuid() == 0 and not root.exists()
        qualified_runner(root/'home/.cursor', inputs['machineID'])
        os.umask(0o077)
        root.mkdir(mode=0o700)
        for n in ['home','cache','tmp','workspace','evidence','recipe']:
            (root/n).mkdir(mode=0o700)
        (root/'home/.cursor').mkdir(mode=0o700)
        (root/'recipe/native_case.py').write_bytes((HERE/'native_case.py').read_bytes())
        (root/'recipe/authoritative_readback.go').write_bytes(READBACK_SOURCE_BYTES)
        outer_peer,inner_peer = socket.socketpair(socket.AF_UNIX,socket.SOCK_STREAM)
        command = ['/usr/bin/unshare','--net','--pid','--fork','--mount-proc','--kill-child=KILL',
                   '/usr/bin/python3','-B','-S',str(root/'recipe/native_case.py'),'--execute','--inside',
                   '--inputs',args.inputs,'--consent',CONSENT,'--old-net',os.readlink('/proc/self/ns/net'),
                   '--old-pid',os.readlink('/proc/self/ns/pid'),'--control-fd',str(inner_peer.fileno()),
                   '--outer-deadline',str(CASE_CLOCK.outer)]
        subprocess.run(['/usr/bin/chown','-R','1000:1000',str(root)],check=True,timeout=CASE_CLOCK.wait(5))
        with (root/'evidence/harness.stdout').open('xb') as out, (root/'evidence/harness.stderr').open('xb') as err:
            child = subprocess.Popen(command,env={'PATH':'/usr/bin:/bin','HOME':str(root/'home'),'LANG':'C.UTF-8'},
                                     stdout=out,stderr=err,start_new_session=True,cwd=root/'workspace',pass_fds=(inner_peer.fileno(),))
            inner_peer.close()
            observer = OuterObservation(child,outer_peer,inputs,CASE_CLOCK)
            forced = False
            outer_error = None
            try:
                while child.poll() is None:
                    assert time.monotonic() < CASE_CLOCK.outer, 'original outer T0+110 deadline'
                    if observer.trace is not None and getattr(observer.trace,'error',None):
                        raise observer.trace.first_error or AssertionError(observer.trace.error)
                    if observer.session is not None and getattr(observer.session,'error',None):
                        raise AssertionError(observer.session.error)
                    if observer.closed:
                        child.wait(timeout=max(.001,CASE_CLOCK.outer-time.monotonic()))
                        break
                    if select.select([outer_peer],[],[],min(.05,max(0,CASE_CLOCK.outer-time.monotonic())))[0]:
                        observer.request()
                code = child.returncode
                forced = code != 0 or not observer.closed
            except Exception as error:
                outer_error = type(error).__name__+':'+str(error)[:240]
                forced = True
                try:
                    print('outer-observation-failure',type(error).__name__[:64],source_failure_frames(error,main.__code__),file=sys.stderr,flush=True)
                except Exception:
                    pass
                if child.poll() is None: os.killpg(child.pid,signal.SIGKILL)
                if observer.trace is not None and hasattr(observer.trace,'tracer'):
                    observer.trace.force_contain(error)
                child.wait(timeout=max(.001,CASE_CLOCK.outer+5-time.monotonic())); code = 1
            finally:
                outer_peer.close()
                # FAIL samples; no PASS/budget.
                if forced:
                    owned = []
                    if observer.trace is not None and hasattr(observer.trace,'tracer'):
                        owned.append((observer.trace.child,observer.trace.tracer))
                    if observer.session is not None:
                        owned += [(p['child'],p['identity']) for p in getattr(observer.session,'processes',[])+getattr(observer.session,'control_processes',[])]
                    def terminal_owned(process,identity):
                        if process.pid != identity['pid']: raise AssertionError('owned cleanup Popen PID drift')
                        with (pathlib.Path('/proc')/str(process.pid)/'stat').open('rb') as held_stat: raw = held_stat.read(8193)
                        if len(raw) > 8192: raise AssertionError('owned cleanup stat budget')
                        text = raw.decode('ascii','strict'); tail = text[text.rindex(')')+2:].split()
                        if int(text[:text.index('(')].strip()) != identity['pid'] or int(tail[19]) != identity['birth']: raise AssertionError('owned cleanup birth drift')
                        return tail[0] == 'Z'
                    for process,identity in owned:
                        if process.poll() is None and not terminal_owned(process,identity):
                            try:
                                if identity.get('startup') is True:
                                    startup_identity(identity['pid'],identity)
                                else:
                                    proc_identity(identity['pid'],identity)
                            except Exception:
                                if not terminal_owned(process,identity): raise
                            else:
                                if process.poll() is None: os.kill(identity['pid'],signal.SIGKILL)
                        process.wait(timeout=max(.001,CASE_CLOCK.outer+5-time.monotonic()))
                    if observer.trace is not None and hasattr(observer.trace,'tracer'):
                        observer.trace.failure_join(CASE_CLOCK.outer+5)
                    monitor_worker = getattr(observer.session,'monitor_pump',None)
                    if monitor_worker is not None:
                        monitor_worker.join(timeout=max(0,CASE_CLOCK.outer+5-time.monotonic()))
                        if monitor_worker.is_alive(): outer_error = (outer_error or '')+';monitor drain timeout'
                    for t in getattr(observer.session,'pumps',[]):
                        t.join(timeout=max(0,CASE_CLOCK.outer+5-time.monotonic()))
                        if t.is_alive(): outer_error = (outer_error or '')+';stderr drain timeout'
                if not (root/'evidence/result.json').exists():
                    (root/'evidence/result.json').write_text(json.dumps({'status':'FAIL','failure':outer_error or 'isolation/startup before native receipt'}))
                assert time.monotonic() <= CASE_CLOCK.outer+5, 'forced containment maximum T0+115'
        if code != 0 or forced:
            result = strict_json((root/'evidence/result.json').read_text())
            result['status'] = 'FAIL'; result['failure'] = result.get('failure') or outer_error or 'outer namespace containment/join failure'
            (root/'evidence/result.json').write_text(json.dumps(result,indent=2))
        (root/'evidence/namespace-join.json').write_text(json.dumps({'outerExit':code,'namespaceJoined':child.poll() is not None,'forcedContainment':forced}))
        return code if code != 0 else int(forced or strict_json((root/'evidence/result.json').read_bytes()).get('status') != 'PASS')
    assert os.geteuid() == 0 and os.getpid() == 1
    assert os.readlink('/proc/self/ns/net') != args.old_net and os.readlink('/proc/self/ns/pid') != args.old_pid
    subprocess.run(['/usr/sbin/ip','link','set','lo','up'],check=True,timeout=CASE_CLOCK.wait(5))
    assert [q['ifname'] for q in strict_json(subprocess.check_output(['/usr/sbin/ip','-j','link'],timeout=CASE_CLOCK.wait(5)))] == ['lo']
    assert strict_json(subprocess.check_output(['/usr/sbin/ip','-j','route'],timeout=CASE_CLOCK.wait(5))) == []
    assert strict_json(subprocess.check_output(['/usr/sbin/ip','-j','-6','route'],timeout=CASE_CLOCK.wait(5))) == []
    os.setgroups([]); os.setgid(1000); os.setuid(1000); os.umask(0o077)
    assert ctypes.CDLL(None).prctl(36,1,0,0,0) == 0
    # Primary 25264136B; cap N+1.
    assert (pathlib.Path(inputs['stage'])/'claude-notifications-linux-amd64').stat().st_size <= TEST_RUNTIME_FILE_LIMIT, 'runtime copy exceeds fixed per-file cap'
    resource.setrlimit(resource.RLIMIT_CORE,(0,0)); resource.setrlimit(resource.RLIMIT_FSIZE,(TEST_RUNTIME_FILE_LIMIT,TEST_RUNTIME_FILE_LIMIT))
    observation = ControllerObservation(socket.socket(fileno=args.control_fd),CASE_CLOCK)
    server, thread, records, webhook_url = owned_webhook(root)
    try:
        installed, foreign = install_and_confirm(root, artifact, inputs, webhook_url)
        assert own_processes() == [next(p for p in own_processes() if p[0] == 1)], 'setup children unresolved'
        primary,selector,binding,generation = installed
        reply = observation.exchange('prepare',controller=proc_identity(1),primary=str(primary),selector=str(selector),binding=binding,generation=generation)
        SESSION_ENV = reply['environment']
        assert set(SESSION_ENV) == {'DBUS_SESSION_BUS_ADDRESS','DISPLAY','XAUTHORITY'}
        enabled = native_run(root, artifact, installed, 'enabled-native',CASE_CLOCK,observation)
        assert enabled['result']['status'] == 'PASS', enabled['result']['failure']
        restarted = finish_lifecycle(root,artifact,inputs['package'],installed,enabled,observation,CASE_CLOCK,records,foreign)
        assert own_processes() == [next(p for p in own_processes() if p[0] == 1)], 'final native/CLI descendants unresolved'
        closed = observation.exchange('close')
        CASE_CLOCK.cleanup_used = closed['cleanupUsed']
        (root/'evidence/result.json').write_text(json.dumps({'status':'PASS','phases':[enabled['result'],restarted['result']],
            'entry':'installed-USER','GUIVisibility':'NOT_RUN'},indent=2)+'\n')
        return 0
    finally:
        began = time.monotonic(); deadline = CASE_CLOCK.cleanup_deadline()
        server.shutdown(); server.server_close(); thread.join(timeout=min(1,max(0,deadline-time.monotonic())))
        assert not thread.is_alive(), 'owned webhook join'
        assert not server.case_failures, 'owned webhook failed; do not retry unknown effects'
        CASE_CLOCK.charge_cleanup(began)


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    operation = parser.add_mutually_exclusive_group(required=True)
    operation.add_argument('--execute', action='store_true')
    operation.add_argument('--validate', action='store_true')
    parser.add_argument('--inputs')
    parser.add_argument('--consent')
    parser.add_argument('--inside', action='store_true')
    parser.add_argument('--old-net'); parser.add_argument('--old-pid')
    parser.add_argument('--control-fd',type=int); parser.add_argument('--outer-deadline',type=float)
    try:
        sys.exit(main(parser.parse_args()))
    except IncompleteInstalledContract as error:
        print(json.dumps({'status':'INCOMPLETE_INSTALLED_OBSERVATION','nativeE':'NOT_RUN',
                          'reason':str(error)}), file=sys.stderr)
        sys.exit(2)
