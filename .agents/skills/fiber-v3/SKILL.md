---
name: fiber-v3
description: Use when writing, changing or reviewing Go HTTP services built on Fiber v3 (github.com/gofiber/fiber/v3) — routing and groups, request binding and validation, middleware selection and ordering, the single error handler, hooks and graceful shutdown, security middleware, or handler tests. Triggers: "Fiber handler", "fiber.New", "app.Use", "c.Bind", "StructValidator", "middleware order", "ErrorHandler", "app.Shutdown", "graceful shutdown", "rate limit this endpoint", "Fiber v3".
allowed-tools: Bash(bash:*)
---

# Fiber v3 — Industry-Grade Practices

Produce and change Fiber v3 code so the result is idiomatic, secure by default, and cheap to operate. This skill is a **generator first**: the rules below decide what to write, the `references/` files carry the depth, and `scripts/audit.sh` mechanically catches the regressions a reader would otherwise miss.

Verified against **Fiber v3.6.1**. Fiber's API moved a lot between v2 and v3, so never assume a v2 idiom still exists — the pin above plus the re-verification pointers at the bottom are how this skill stays honest.

## Resource Routing

| Resource | Read it when |
|---|---|
| `references/routing-and-groups.md` | Declaring routes, groups, mounts, constraints, named routes, or `Domain()` routing |
| `references/binding-and-validation.md` | Binding request data, designing DTOs, wiring `StructValidator`, shaping 422 bodies |
| `references/middleware-and-order.md` | Choosing middleware, deciding its order, or debugging why one does not run |
| `references/errors-and-hooks.md` | Writing the `ErrorHandler`, wrapping errors, or placing startup/shutdown work in hooks |
| `references/security.md` | Hardening headers, CORS, CSRF, body limits, cookies, client IP handling |
| `references/lifecycle-and-testing.md` | Configuring the app, probes, timeouts, handler and middleware tests, shutdown behaviour |
| `scripts/audit.sh` | Before finishing any Fiber change — run it on the touched packages |

## Decision rules

These are the rules that most often decide whether a Fiber service is pleasant or painful. Each is one line of judgement; the linked reference argues it.

**Routing**
1. Register static paths before parameterised ones — `/users/me` before `/users/:id` — because Fiber matches in registration order and the first match wins.
2. Constrain a parameter at the route (`:id<int>`) instead of validating it inside the handler; a non-matching path then 404s without entering your code. Register your own with `app.RegisterCustomConstraint`.
3. Give every route a name (`.Name("users.show")`) and build outbound URLs from `app.GetRoute(name).URL(...)` rather than concatenating strings in handlers.
4. Put shared middleware on the group it belongs to (`app.Group("/admin", auth)`) instead of repeating it per route.

**Binding and validation**
5. Bind into a transport struct, never into the domain or persistence model. One struct per direction (`*Body`, `*PathParams`, `*QueryParams`, `*Resource`).
6. Always `c.Bind().Body(&req)` / `.Query(&req)` / `.URI(&req)`. Reaching for `c.Params("id")` or `c.Query("page")` bypasses validation and the 422 shape.
7. Validate with the `StructValidator` seam, not with checks scattered through the handler; return a field→messages map so the client can fix every problem in one round trip.
8. Never do database I/O in a validator. A "does this value already exist" check inside validation is a TOCTOU race that a unique index would catch anyway — validate shape, let the write decide uniqueness.
9. Tag JSON fields camelCase and keep nullable fields as pointers so "absent" and "empty" stay distinguishable.

**Middleware and order**
10. Order by cost and by rejection: `recover` → request id → logging/tracing → security headers → CORS → CSRF → rate limit → auth → body/route handlers. A request that will be rejected must be rejected before anything expensive runs.
11. `limiter` uses a fixed window by default; for anything user-facing set `LimiterMiddleware: limiter.SlidingWindow{}` so a burst cannot straddle a window boundary.
12. Never key a limiter on `c.IP()` alone behind a proxy — it collapses every client into the proxy address. Configure `TrustProxy`/`ProxyHeader` deliberately, or reject the header at the edge.
13. `compress`/`etag` belong after the handlers that produce the body; keep them off streaming endpoints (`SendStream`, SSE) where buffering defeats the purpose.

**Errors and lifecycle**
14. Install exactly one `ErrorHandler`, and make it the only place that maps an error to a status. Handle the error there once — never log and return the same error.
15. Classify by type, not by string: an application error carrying an explicit code/message, `*fiber.Error` for framework-generated ones, everything else → 500 with a generic message plus a logged cause. Internals must never reach the response.
16. Keep 4xx out of error-level logs. A 404 or a 422 is normal traffic; logging it as an error trains people to ignore the log.
17. `app.Listen` returns and blocks: when you need `app.Shutdown()` (any shutdown hook, any graceful drain) run `Listen` in a goroutine.
18. Put drain-free work in `OnPreShutdown` (stop consumers, flip readiness to 503) and release work in `OnPostShutdown` (close pools, flush telemetry). `OnPostShutdown` receives the shutdown error — decide explicitly whether it is fatal.
19. Decide the config **before** `fiber.New` and pass it in; treat the config as immutable afterwards so no handler can change global behaviour at runtime.
20. `/livez` answers "is this process alive" and must never touch a dependency; `/readyz` answers "should traffic come here" and may fail on a critical dependency (503) while reporting a degraded-but-serving state for a non-critical one.

## Workflow

1. Locate the change: new route/group, new middleware, a handler edit, or a lifecycle change. Read the matching reference(s) from the table above before writing code.
2. Write the code with the decision rules applied. Prefer an explicit type over a loose `any`; prefer a named constant over a literal status.
3. Run `bash scripts/audit.sh <packages…>` on what you touched and fix every FAIL it reports. WARN lines are judgement calls — read the reason and decide.
4. Verify the behaviour, not just compilation: hit the route through a test with `app.Test` (see `references/lifecycle-and-testing.md`) and assert the **error body shape**, not only the status code.
5. If the API does not look like what this skill claims, re-verify against Context7 (below) and treat this skill as stale for that area.

## Re-verification pointers

- Fiber docs + guides: Context7 library `/websites/gofiber_io`
- Fiber source-level questions (what a method really does): Context7 library `/gofiber/fiber`
- Contrib middleware (otel, swagger, websocket, jwt): Context7 library `/gofiber/contrib`
- Pin of record for this skill: **fiber v3.6.1**

Claims that are engineering judgement rather than something the Fiber docs state are marked **practice claim** in the references. When one of those conflicts with your project's own convention, the project wins.

## Related skills

- **`go-code-review`** (user-level skill under `~/.codebuddy/skills/`) — general Go style, error and naming review to run alongside this one
- **`bun-orm`** — the persistence half: handlers must not own SQL, and transactions travel through `context.Context`

Referenced by name rather than by relative link on purpose: a relative link only resolves inside one skills root, and this skill's root differs per installation (user-level versus a project's `.agents/skills/`). A name cannot dangle.
