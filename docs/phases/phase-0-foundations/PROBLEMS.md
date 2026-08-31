# Phase 0 problems

Things that broke, what the cause turned out to be, and what fixed them. Date
every entry. A problem that got worked around rather than fixed says so.

## 2026-08-31: dependency go directives are ahead of the toolchain

`GOPROXY=off go mod download` fails with `module
github.com/modelcontextprotocol/go-sdk@v1.7.0 requires go >= 1.25.0 (running
go 1.24.6)`. Not fixed, deferred. See `DECISIONS.md` for the three ways out
and why none was taken during the bootstrap.
