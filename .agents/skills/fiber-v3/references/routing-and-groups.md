# Routing, groups and mounts

## Registration order decides matching

Fiber matches routes in the order they were registered. A parameterised route registered first will swallow requests meant for a static sibling.

```go
app.Get("/users/me", meHandler)   // wins
app.Get("/users/:id", userHandler)
```

If the two are swapped, `GET /users/me` reaches `userHandler` with `id == "me"`. Two defences, use both:

1. Register static before parameterised.
2. Constrain the parameter so the wrong shape cannot match at all: `app.Get("/users/:id<int>", …)`.

## Constraints

Built-in constraint names are declared inline after the parameter:

```go
app.Get("/users/:id<int>", handler)
app.Get("/files/*", handler)          // splat
app.Get("/:id?", handler)             // optional
app.Get("/:id<int>?", handler)        // optional + constrained
```

A splat (`*`) exposes the remainder through `c.Params("*")`. Optional parameters make two shapes share one route — decide whether the empty case is a valid request or a 404 before reaching for one.

Register a custom constraint when the shape is domain-specific (a ULID, a slug, a currency code):

```go
type ULID struct{ fiber.CustomConstraint }

func (*ULID) Name() string { return "ulid" }
func (*ULID) Execute(param string, args ...string) bool {
    _, err := ulid.Parse(param)
    return err == nil
}

app.RegisterCustomConstraint(&ULID{})
app.Get("/items/:id<ulid>", handler)
```

**Practice claim**: prefer a constraint over an in-handler `if !valid(param)` — the constraint keeps the rejection out of your handler and out of your metrics' latency budget.

## Groups

```go
api := app.Group("/v1")
admin := api.Group("/admin", authMiddleware, auditMiddleware)
admin.Get("/users", listUsers)
```

- Middleware passed to `Group` runs for every route in that group; put auth and authorisation there, not on each route.
- Groups nest; paths compose.
- A group is not a separate app — its middleware still runs in the global chain's order.

## Mounting and domains

Mount a sub-app when a subsystem owns its own middleware stack:

```go
api := fiber.New()
api.Get("/users", listUsers)
app.Use("/api", api)          // v3 idiom
```

`app.Use(path, subApp)` is the current way to mount. `app.Mount` is the older spelling — do not start new code on it.

Domain-scoped routing keeps multi-tenant or host-based routing out of the path space:

```go
app.Domain("api.example.com").Get("/users", handler)
app.Domain(":tenant.example.com").Get("/", func(c fiber.Ctx) error {
    return c.SendString(fiber.DomainParam(c, "tenant"))
})
```

**Practice claim**: reach for `Domain` only when the host genuinely carries meaning; otherwise a path prefix is easier to test.

## Named routes and URL generation

Name routes and generate URLs from the router so a path change cannot leave stale strings behind:

```go
app.Get("/user/:name/:id", handler).Name("user.show")

route := app.GetRoute("user.show")
location, _ := route.URL(fiber.Map{"name": "john", "id": 1}) // /user/john/1
```

Use naming for anything a client will follow: redirect targets, `Location` headers, links inside emails. Store route names as constants.

## Config that changes routing

`CaseSensitive`, `StrictRouting` and `Immutable` are read at `fiber.New`. Decide them up front:

- `CaseSensitive: true` — `/Users` and `/users` differ.
- `StrictRouting: true` — `/users/` and `/users` differ.
- `Immutable: true` — returned strings are copies, so they survive the request; without it, do not retain anything derived from the request.

Changing these after construction has no effect on already-registered routes. **Practice claim**: pick strict, case-sensitive routing for APIs; permissive matching hides client bugs.

## Checklist

- [ ] Static routes registered before parameterised ones
- [ ] Every path parameter that has a shape carries a constraint
- [ ] Auth/authorisation attached at the group, not repeated per route
- [ ] Outbound URLs built from `GetRoute(name).URL(...)`, names held as constants
- [ ] `CaseSensitive`/`StrictRouting`/`Immutable` decided before `fiber.New`
