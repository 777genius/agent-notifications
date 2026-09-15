# uapinstaller external sample

Separate Go module. No `replace`, no `internal` imports, no raw Store/Kernel
wiring. Constructor proof:

```sh
GOWORK=off go get github.com/777genius/agent-notifications/install/uapinstaller@<commit>
GOWORK=off go run .
```

Prints `external import ok` and does not create `/uapinstaller-sample-state`.

Install → inspect → repeat → remove requires explicit absolute roots and a
standard local package plus helper:

```sh
GOWORK=off go run . -state /abs/uap -package /abs/pkg -config /abs/config \
  -helper /abs/helper -client-exe /abs/client
```
