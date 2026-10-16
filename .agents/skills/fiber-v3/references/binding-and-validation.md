# Binding and validation

## Bind into transport structs, one per direction

```go
type CreateUserBody struct {
    Name  string `json:"name"  validate:"required,min=2"`
    Email string `json:"email" validate:"required,email"`
}

type ListUsersQueryParams struct {
    Page     int    `query:"page"     validate:"omitempty,min=1"`
    PageSize int    `query:"pageSize" validate:"omitempty,min=1,max=100"`
    Sort     string `query:"sort"     validate:"omitempty,max=120"`
}

type UserPathParams struct {
    ID string `uri:"id" validate:"required"`
}

type UserResource struct {
    ID    string `json:"id"`
    Name  string `json:"name"`
    Email string `json:"email"`
}
```

Rules that keep these useful:

- **Never bind into a domain or persistence model.** A payload that reaches your entity lets a client set fields the endpoint never intended to expose.
- One struct per direction and per operation. Sharing one "Update" struct between create and patch forces every field to be optional and loses the type-level signal about what the caller may send.
- Suffix by role (`*Body`, `*PathParams`, `*QueryParams`, `*Resource`) so a reader knows the direction at the call site.
- camelCase JSON/query tags. Pointers for genuinely optional scalars so "absent" and "zero" stay distinguishable; a non-pointer `bool` cannot express "not provided".

## Bind through the framework

```go
var body CreateUserBody
if err := c.Bind().Body(&body); err != nil {
    return err                     // the ErrorHandler classifies it
}

var params UserPathParams
if err := c.Bind().URI(&params); err != nil {
    return err
}

var query ListUsersQueryParams
if err := c.Bind().Query(&query); err != nil {
    return err
}
```

`Bind()` also offers `.Header(…)` and `.Cookie(…)`. Reaching past it (`c.Params("id")`, `c.Query("page")`, manual `json.Unmarshal(c.Body())`) skips the validator and the consistent error shape — treat it as a defect rather than a shortcut.

`Bind()` runs the configured `StructValidator`. Disable that automatic handling only when you intend to own the response yourself, and do it per binding: `c.Bind().WithoutAutoHandling()` on the call that follows (`WithAutoHandling()` turns it back on). Mixing automatic and manual validation produces two error shapes for one API.

## Validation seam

Install a `StructValidator` once, on the app config:

```go
app := fiber.New(fiber.Config{StructValidator: myStructValidator{}})
```

The interface is deliberately narrow:

```go
type StructValidator interface {
    Validate(out any) error
}
```

What a good implementation does:

1. Runs tag validation, then any command-style hook the struct implements (a `Validate() error` method).
2. **Reports both sets of failures, not just the first.** A tag failure on `pageSize` must not mask a hook failure on `sort` — otherwise the client fixes one field, retries, and is told about the next.
3. Translates messages into a stable field→messages map, so the handler can return one 422 body listing everything.

```go
// shape the API returns, whatever the validator is
{
  "code": "VALIDATION_FAILED",
  "message": "The given data was invalid",
  "errors": { "pageSize": ["must be at most 100"], "sort": ["unknown column"] }
}
```

Keep the 422 shape identical everywhere. `errors` keyed by the **client's** field name (the JSON/query name), never the Go field name.

## What not to validate here

- **No database I/O.** `unique`/`exists` style rules inside the validator query the database, then the write happens later: a race window that a unique index closes properly. Validate shape here; let the write report the conflict, mapped to a 409.
- **No authorisation.** "May this caller change this row" is not a payload-shape question and does not belong in a field rule.
- **No side effects.** Ordering, normalisation that must persist, event emission — all belong after validation succeeded.

## Normalise, then validate

Trim strings and canonicalise case **before** the struct's own check runs, so `"  Alice "` and `"alice@example.com"` behave the way the rules assume. Doing it after validation means `min=2` can pass on whitespace. **Practice claim**: expose the normalisation in the DTO's own hook so it cannot be forgotten in one handler.

## Checklist

- [ ] Binding targets a transport struct, never a domain/persistence model
- [ ] `Bind().Body/.Query/.URI` used; no `c.Params`/`c.Query` for validated input
- [ ] `StructValidator` reports tag **and** hook failures together
- [ ] The 422 body is one shape, keyed by client field names
- [ ] No DB access, no authorisation and no side effects inside validation
- [ ] Strings trimmed / case normalised before length and format rules run
