## What and why

<!-- One paragraph: what changes, and the problem it solves. -->

## Checklist

- [ ] `go test ./...`, `go test -race ./...`, `go vet ./...`, and `gofmt -l .` pass.
- [ ] The change is one logical unit.
- [ ] New behavior has a test that fails without it.

<!--
Adding a provider? Check every box in docs/providers.md and paste the
providercheck.Check call from the provider's tests here:

```go
providercheck.Check(t, New(), roots, providercheck.Expect{...})
```
-->

## How to test

<!--
Concrete, reproducible steps a reviewer can run. Throwaway shell/REPL commands
are fine; a session, database, or account the reviewer can borrow is better.
-->
