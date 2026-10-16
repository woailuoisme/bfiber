# Security defaults

Fiber ships the middleware; the security work is in the configuration and, above all, in the ordering. Enable these by default, then relax deliberately.

## Header, origin and CSRF stack

```go
app.Use(helmet.New(helmet.Config{
    HSTSMaxAge:            63072000,
    HSTSPreloadEnabled:    true,
    ContentSecurityPolicy: "default-src 'self'",
}))

app.Use(cors.New(cors.Config{
    AllowOrigins:     []string{"https://app.example.com"}, // exact origins, never "*" with credentials
    AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE"},
    AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Csrf-Token"},
    AllowCredentials: true,
    MaxAge:           3600,
}))

app.Use(csrf.New(csrf.Config{
    CookieName:        "__Host-csrf_",
    CookieSecure:      true,
    CookieHTTPOnly:    false, // the SPA must read it to echo it back
    CookieSameSite:    "Lax",
    CookieSessionOnly: true,
    Extractor:         extractors.FromHeader("X-Csrf-Token"),
}))
```

- `AllowOrigins: ["*"]` together with `AllowCredentials: true` is not a permissive policy, it is a broken one — browsers reject the combination, and any reflection bug turns it into a real hole. List origins exactly.
- `__Host-` prefix (and `Secure` + `SameSite`) is what stops a subdomain from planting the cookie.
- Enable CSRF only for cookie-authenticated surfaces; a pure bearer-token API does not need it (and adding it there just breaks clients).
- **Practice claim**: keep the allowed-origin list in config, not in code — the environments differ and a hard-coded production origin in a repo is a leak waiting to be copied.

## Body and upload limits

Set a global ceiling and a tighter per-route one where uploads happen:

```go
app := fiber.New(fiber.Config{BodyLimit: 1 << 20})   // 1 MiB default ceiling
uploads := app.Group("/uploads", ...)                 // route-specific ceilings live with the route
```

- A request without a bound is a memory-exhaustion vector.
- The limit must be ≥ the largest legitimate payload, so pick it from the payload, not from a guess; the framework answers oversize with a 413 which your `ErrorHandler` should render in the standard body shape.
- Validate uploads beyond size: content sniffing (magic bytes), an allow-list of formats, and a decode that fails closed. A `Content-Type` header is client-supplied and proves nothing.

## Client identity behind a proxy

The single most common rate-limiting bug: keying on `c.IP()` while the app sits behind a proxy, so every request shares the proxy's address and one client can exhaust everyone's budget.

- Configure the trusted proxy chain (`TrustProxy`, `ProxyHeader`) explicitly, or terminate and rewrite the header at the edge.
- Never trust `X-Forwarded-For` blindly: it is a client-supplied list, so take the right-most untrusted hop, not the left-most value.
- Prefer an authenticated identity (subject, API key) over an address for anything that must not be evaded — addresses move and NAT exists.

## Cookies and sessions

- `Secure`, `HTTPOnly` (except a CSRF cookie that the SPA must read), `SameSite=Lax` or `Strict`, and an explicit `MaxAge`.
- Rotate the session identifier on privilege change (login, elevation) to close session fixation.
- Sessions need shared storage (`Storage:`) in a multi-instance deploy — an in-memory store silently forgets sessions on deploy and cannot be shared.

## Logging and responses

- Never log credentials, tokens, cookies, or the full body of an auth request.
- Sanitise anything echoed back. An error message that quotes a client-supplied field can become reflected XSS if it lands in HTML, and a leak if it quotes a query.
- Return 404 rather than 403 when the existence of a resource is itself sensitive, and a constant-shape error for credential failures so account enumeration stays impractical.
- **Practice claim**: keep a single "public message" concept on your error type; the moment a handler formats a message from raw input, sanitisation becomes everyone's job.

## Middleware ordering is a security property

`helmet` and `cors` must run **before** the code that can reject a request, so that even a 401/429 carries the security headers and the CORS headers a browser needs to read the response. A `limiter` placed after an expensive auth check protects nothing.

## Checklist

- [ ] helmet enabled with HSTS; CSP set deliberately
- [ ] CORS: exact origins, no wildcard with credentials, `MaxAge` set
- [ ] CSRF for cookie auth, with `__Host-` + `Secure` + `SameSite`
- [ ] Global `BodyLimit` plus per-route limits on uploads; uploads validated by content, not header
- [ ] Client identity behind a proxy resolved deliberately; `X-Forwarded-For` not trusted blindly
- [ ] Cookies: `Secure`, `HTTPOnly` where possible, `SameSite`, explicit expiry
- [ ] No secrets or raw input in logs or response bodies
- [ ] helmet/cors ordered before anything that can reject
