# Middleware selection and ordering

## The canonical order for a JSON API

```go
app := fiber.New(fiber.Config{ /* see lifecycle-and-testing */ })

app.Use(recover.New())                       // 1. a panic must become a 500, not a dead connection
app.Use(requestid.New())                     // 2. everything after this can be correlated
app.Use(otelfiber.Middleware())              // 3. tracing/logging wraps the rest of the chain
app.Use(helmet.New())                        // 4. security headers on every response, including errors
app.Use(cors.New(...))                       // 5. preflight must be answered before auth
app.Use(csrf.New(...))                       // 6. only if cookie-authenticated
app.Use(limiter.New(...))                    // 7. reject cheap, before auth work
app.Use(auth.New(...))                       // 8. identity for the routes below
app.Use(idempotency.New(...))                // 9. after identity: keyed per caller
```

Two principles decide anything not in that list:

- **Reject before you spend.** Order by "how cheap is it to reject here", so an unauthenticated flood never reaches the code that does work.
- **Wrap, don't sprinkle.** Recovery, request-id and tracing must wrap *everything*, including the middleware that reject requests.

## Middleware worth configuring rather than defaulting

**Recover.** `recover.New()` turns a panic into a 500 and (by default) logs it. A recovered panic is a bug, not a control-flow tool: never `panic` in a handler to signal a bad input. Relevant option: `EnableStackTrace`.

**Rate limiting.** Defaults are a fixed window of 5 requests per minute keyed on `c.IP()`.

```go
app.Use(limiter.New(limiter.Config{
    Max:               20,
    Expiration:        30 * time.Second,
    LimiterMiddleware: limiter.SlidingWindow{},
    KeyGenerator:      func(c fiber.Ctx) string { return clientKey(c) },
}))
```

- Use `SlidingWindow` for anything a real user can hit: a fixed window lets a burst straddle the boundary and get 2× the allowance.
- `KeyGenerator` must identify the **caller**, not the hop. Behind a proxy, `c.IP()` is the proxy — configure `TrustProxy`/`ProxyHeader` deliberately or key on an authenticated subject/API key instead.
- Give login/OTP endpoints a stricter config than the global one (`ExpirationFunc`/`MaxFunc` exist for exactly this).
- Storage-backed (`Storage:`) limiting only works if every instance shares the storage; with in-memory storage a multi-instance deploy multiplies the limit by the instance count.

**Idempotency.** For unsafe methods that clients retry, use the idempotency middleware with shared storage and a lifetime that matches how long a client may retry. Order it after auth so keys are per caller.

**Timeout.** A per-request timeout stops one slow dependency from pinning a connection. Note the interaction with streaming endpoints: a timeout that applies to the whole response will cut a legitimate long-lived stream.

**Compress / ETag.** Correct for JSON and static assets, wrong to blanket-apply to streams and already-compressed payloads. Enable compression after the handlers that produce bodies, and skip server-sent events.

## Where middleware state goes

```go
// private key type: no other package can collide with it
type userKey struct{}

func currentUser(c fiber.Ctx) (*User, bool) {
    u, ok := c.Locals(userKey{}).(*User)
    return u, ok
}
```

- `Locals` is request-scoped and removes the value afterwards (calling `Close` if it implements `io.Closer`).
- Use a **private type** as the key. A string key is a shared global namespace: any package can overwrite it, and the failure shows up as a type assertion panic far from the cause.
- `State` is app-scoped and survives requests (pooled contexts); use it for read-mostly handles, never for per-request data.
- `fiber.StoreInContext` stores a value in both `Locals` and the request `context.Context` when downstream code only has a `context.Context`.

## Debugging "my middleware doesn't run"

In order: (1) it was added **after** a middleware that short-circuits (`c.Send…` without `c.Next()`), (2) it was added to a group that does not contain the route, (3) the route was registered **before** the `app.Use` that was meant to cover it, (4) a `Next`/`Skip` predicate is filtering it out. Registration order is the usual answer.

## Checklist

- [ ] recover → request id → trace/log → helmet → cors → csrf → limiter → auth → idempotency
- [ ] Rate limiter keys on the real client, uses a sliding window, and is stricter on auth endpoints
- [ ] Idempotency uses shared storage and runs after auth
- [ ] `compress`/`etag` not applied to streaming endpoints; request timeout not applied to streams
- [ ] Request-scoped values keyed by private types, mutex-free, no `State` for per-request data
