# Lifecycle, probes and testing

## Config is decided before construction

```go
app := fiber.New(fiber.Config{
    AppName:               "orders-api",
    BodyLimit:             1 << 20,
    ReadTimeout:           5 * time.Second,
    WriteTimeout:          10 * time.Second,
    IdleTimeout:           120 * time.Second,
    CaseSensitive:         true,
    StrictRouting:         true,
    Immutable:             true,
    StructValidator:       myStructValidator{},
    ErrorHandler:          myErrorHandler,
    DisableStartupMessage: cfg.Env == "production",
})
```

- Everything above is read at construction. Mutating `app.Config()` afterwards changes nothing for already-registered routes, so treat the config as immutable and pass the values in.
- `Immutable: true` is required if any value derived from the request (a string from `c.Params`, a header) outlives the handler — for example, is stored on a struct or sent to another goroutine. Without it, the buffer is reused and the value is silently corrupted. This is the sharpest edge of a fasthttp-based framework.
- Set the three timeouts. Without `WriteTimeout` a slow client keeps a connection indefinitely; without `IdleTimeout` keep-alive connections accumulate.
- `ReadTimeout` covers the whole request read: on upload endpoints it must accommodate a legitimately slow upload, or move those routes off the global value.
- `DisableStartupMessage` in production keeps the banner out of structured logs.

## Probes

Two endpoints, two different questions:

```go
// liveness: "is this process alive?" — never touches a dependency
app.Get("/livez", func(c fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })

// readiness: "should traffic come to me?"
app.Get("/readyz", func(c fiber.Ctx) error {
    if err := db.Ping(c.Context()); err != nil {
        return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"status": "unavailable"})
    }
    return c.SendStatus(fiber.StatusOK)
})
```

- A liveness probe that queries the database turns a database blip into a restart loop that cannot help.
- Readiness may fail (503) for a critical dependency; for a non-critical one, report `degraded` with 200 so the orchestrator keeps routing.
- **Practice claim**: give each dependency an explicit critical/non-critical label in config, so the probe's behaviour is a decision rather than an accident of who wrote the check.
- Flip readiness to 503 at the start of shutdown so the load balancer drains before the listener closes.

## Testing handlers

Use the framework's own test seam — a real request through the real chain, no network:

```go
app := fiber.New(fiber.Config{ErrorHandler: myErrorHandler})
app.Post("/users", createUser)

req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(body))
req.Header.Set("Content-Type", "application/json")

resp, err := app.Test(req, fiber.TestConfig{Timeout: 5 * time.Second})
```

What to assert, beyond the status code:

1. **The error body shape** — code and the field→messages map. A status-only test passes while the client contract is broken.
2. **That the middleware under test ran** — assert the observable effect (header present, storage written, next handler called) instead of asserting the middleware was registered.
3. **The rejection path of every guard** — unauthenticated, unauthorised, over the rate limit, oversize body. These are the paths that never get hit manually.
4. **Ordering-sensitive behaviour** — e.g. that a 401 still carries the security and CORS headers, which only holds if the ordering is right.

`app.Test` runs the handler chain without binding a port, so it is the right level for handler and middleware tests. Anything that needs a real socket (TLS, HTTP/2, connection draining) belongs in an integration test against a started server.

## Verifying shutdown

Shutdown is behaviour, so test it rather than reading it:

- Start the app on an ephemeral port, send a request that blocks, trigger shutdown, and assert the in-flight request finished and the process exited inside the timeout.
- Assert the readiness flag flips **before** the listener stops (the drain contract).
- Assert the release hooks ran exactly once — a hook registered twice is a double-close waiting for production.

**Practice claim**: the drain window and the shutdown timeout are the two numbers worth asserting explicitly; they are the difference between a deploy with no dropped requests and one with a handful every time.

## Checklist

- [ ] Config built before `fiber.New`; not mutated afterwards
- [ ] `Immutable: true` whenever request-derived strings outlive the handler
- [ ] `ReadTimeout`/`WriteTimeout`/`IdleTimeout` set; upload routes considered separately
- [ ] `/livez` dependency-free; `/readyz` fails only on critical dependencies and flips during drain
- [ ] Handler tests use `app.Test` and assert the error body, not just the status
- [ ] Guard rejection paths covered by tests
- [ ] Shutdown verified with an in-flight request and a timeout
