# Give ports.Parse a maximum

`ports.Parse` currently hard-codes 65535 as the largest valid port. Change it
to `Parse(s string, max int)` so callers choose their own ceiling, and update
every caller. The `server` and `proxy` packages keep accepting up to 65535;
the `admin` package must only accept privileged ports (up to 1023). The
tests in `ports` already describe the new signature; the repository's tests
must all pass afterwards. Do not add a second parse function.
