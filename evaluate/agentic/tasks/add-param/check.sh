#!/bin/sh
# Deterministic gate for add-param. cwd = the agent's workspace copy.
set -e

go vet ./...
go test ./...

# One parse function, with the new signature (no ParseMax-style duplicate).
test "$(grep -c 'func Parse' ports/ports.go)" -eq 1 || { echo "FAIL: expected exactly one Parse func"; exit 1; }
grep -q 'func Parse(s string, max int)' ports/ports.go || { echo "FAIL: Parse signature not updated"; exit 1; }

# Every caller still goes through ports.Parse.
for f in server/server.go proxy/proxy.go admin/admin.go; do
	grep -q 'ports\.Parse(' "$f" || { echo "FAIL: $f no longer calls ports.Parse"; exit 1; }
done

# admin must use the privileged ceiling, not 65535.
if grep -q '65535' admin/admin.go; then echo "FAIL: admin must cap at 1023"; exit 1; fi
