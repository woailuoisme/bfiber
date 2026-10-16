# Errors, the ErrorHandler, and lifecycle hooks

## One handler maps every error to a response

Install exactly one `ErrorHandler`. It is the only place that knows how an error becomes a status code, a body and a log line.

```go
type AppError struct {
    Code    string // stable, machine-readable
    Status  int
    Message string // safe to show a client
    Cause   error  // never serialised
}

func (e *AppError) Error() string { return e.Message }
func (e *AppError) Unwrap() error { return e.Cause }
```

```go
ErrorHandler: func(c fiber.Ctx, err error) error {
    var appErr *AppError
    var fiberErr *fiber.Error

    switch {
    case errors.As(err, &appErr):
        if appErr.Status >= 500 {
            log.Error("request failed", zap.Error(appErr.Cause), zap.String("code", appErr.Code))
        }
        return c.Status(appErr.Status).JSON(errorBody(appErr.Code, appErr.Message))

    case errors.As(err, &fiberErr):
        // framework-generated: 404 from the router, 413 from the body limit, …
        return c.Status(fiberErr.Code).JSON(errorBody(codeFor(fiberErr.Code), fiberErr.Message))

    default:
        log.Error("unhandled error", zap.Error(err))
        return c.Status(fiber.StatusInternalServerError).
            JSON(errorBody("INTERNAL_SERVER_ERROR", "An unexpected error occurred"))
    }
}
```

What this encodes:

1. **Classify by type, never by string matching.** `errors.As` on a typed error survives wrapping; `strings.Contains(err.Error(), "not found")` does not.
2. **One body shape for every failure**, with a stable machine-readable code. Clients switch on the code, humans read the message; neither parses prose.
3. **Log once, at the boundary, and only for 5xx.** Logging in the repository *and* returning the error to be logged again is the classic double-log; and logging 404/422 as errors makes the error log useless. If you log early, do not return; if you return, do not log.
4. **Never leak internals.** A driver error, a SQL fragment or a stack trace in the response is an information disclosure. Keep the cause in the log, keep the message generic.
5. **`*fiber.Error` is real traffic.** The router's 404, the body-limit 413 and `c.SendStatus` errors arrive as `*fiber.Error`; handle them explicitly or they fall into the 500 branch.

## Wrapping so the handler stays dumb

Put the code on the error where it is raised, not where it is rendered:

```go
if err != nil {
    return fmt.Errorf("load user %s: %w", id, &AppError{Code: "USER_LOAD_FAILED", Status: 500, Cause: err})
}
```

**Practice claim**: give every outward code one owner — a single package that declares them — so two endpoints cannot invent two codes for one condition. When a not-found condition has a canonical representation, use that one everywhere instead of a fresh literal per site.

## Hooks: which work belongs where

| Hook | Fires | Use it for |
|---|---|---|
| `OnPreStartupMessage` | the banner is being built | enriching/altering the startup banner with env, version, build sha |
| `OnPostStartupMessage` | the banner has been printed | work that must precede traffic but follow the announcement |
| `OnListen` | right before accepting connections | "I am up" side effects: registering with discovery, starting background workers |
| `OnPreShutdown` | when shutdown begins, still accepting | flip readiness to 503, stop consumers, let a load balancer drain |
| `OnPostShutdown` | after the server stopped | close pools, flush telemetry, release files |

The complete v3.6.1 set is `OnPreStartupMessage`, `OnPostStartupMessage`, `OnListen`, `OnPreShutdown`, `OnPostShutdown`, plus the routing hooks `OnRoute`, `OnName`, `OnGroup`, `OnGroupName`, `OnMount`, `OnFork`. **There is no `OnPreStart`** — code ported from a guide that mentions one is naming a hook that will never fire. Work that must precede the listener belongs before `Listen` is called or inside `OnListen`.

```go
app.Hooks().OnPreShutdown(func() error { stopConsumers(); return nil })
app.Hooks().OnPostShutdown(func(err error) error {
    if err != nil { log.Error("shutdown reported an error", zap.Error(err)) }
    return flushTelemetry(ctx)
})
```

Rules:

- **`OnPostShutdown` decides whether the shutdown error matters.** It receives the error; ignoring it silently is how "clean shutdown" becomes a lie.
- **`Listen` must be running in a goroutine** for `Shutdown` to be reachable. `app.Listen` blocks, so a program that calls it on the main goroutine can never react to a signal.
- Work that holds up new connections belongs in `OnPreShutdown`; work that needs the server stopped belongs in `OnPostShutdown`. Putting a pool close in `OnPreShutdown` can kill in-flight requests that are still being served.
- Hook errors are not request errors: they do not have a request context and cannot be returned to a client. Log them and decide the process's exit status.

## Graceful shutdown, concretely

```go
quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

go func() {
    if err := app.Listen(":3000"); err != nil {
        log.Error("listen failed", zap.Error(err))
    }
}()

<-quit

ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
defer cancel()

done := make(chan error, 1)
go func() { done <- app.Shutdown() }()

select {
case <-done:
case <-ctx.Done():
    log.Error("graceful shutdown timed out; forcing exit")
}
```

- The timeout is the whole point: without it a stuck connection makes the process immortal and the orchestrator has to SIGKILL.
- Flip a readiness flag (and give the load balancer a moment) before calling `Shutdown`, or traffic keeps arriving while you drain.
- **Practice claim**: make the drain window a config value, not a literal — it has to be tuned per deployment.

## Checklist

- [ ] Exactly one `ErrorHandler`; it is the only status mapper
- [ ] Errors classified with `errors.As` on types, with `*fiber.Error` handled explicitly
- [ ] 5xx logged once with the cause; 4xx not logged as errors; no double-log
- [ ] No internal detail in any response body
- [ ] `Listen` in a goroutine when `Shutdown`/shutdown hooks are used
- [ ] `OnPreShutdown` drains, `OnPostShutdown` releases; shutdown error is explicitly judged
- [ ] Shutdown wrapped in a timeout with a forced-exit path
