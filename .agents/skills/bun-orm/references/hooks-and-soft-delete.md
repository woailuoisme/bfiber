# Hooks and soft delete

## Model hooks

Declare the interface assertion so a signature drift fails at compile time instead of silently never firing:

```go
var _ bun.BeforeAppendModelHook = (*User)(nil)

func (u *User) BeforeAppendModel(ctx context.Context, query bun.Query) error {
    switch query.(type) {
    case *bun.InsertQuery:
        now := time.Now().UTC()
        u.CreatedAt = now
        u.UpdatedAt = now
    case *bun.UpdateQuery:
        u.UpdatedAt = time.Now().UTC()
    }
    return nil
}
```

| Hook | Fires | Typical use |
|---|---|---|
| `BeforeAppendModel` | immediately before the model is appended to a query | timestamps, normalisation of fields |
| `BeforeScanRow` / `AfterScanRow` | around scanning one row | decoding a composite column, deriving a field |
| `BeforeInsert` / `AfterInsert` | once per insert statement, resolved on the table's zero value | statement-level audit, refusing a statement |
| `BeforeUpdate` / `AfterUpdate` | once per update statement, same zero-value resolution | refusing a class of update, statement-level audit |
| `BeforeDelete` / `AfterDelete` | once per delete statement, same zero-value resolution | refusing a delete, audit |

Rules worth knowing before you rely on them:

1. **A hook is not a validation layer.** It has no request context beyond what the caller passed, cannot map to an HTTP status, and may run outside any transaction the caller intended. Validate input before the write; use hooks for invariants the row itself must satisfy.
2. **A bulk write runs the entity hooks once per statement, not once per row.** Bun resolves `BeforeInsert`/`AfterInsert` against the table's zero value (`query_insert.go`), so a hook written to see or mutate the rows being inserted receives a zero-valued instance instead. Set per-row values in the slice you build, with an explicit column, or with a database default — not in an insert hook. `BeforeAppendModel` is the hook that does see each appended model.
3. **`BeforeAppendModel` receives the query type** — use the type switch (`*bun.InsertQuery` vs `*bun.UpdateQuery`) rather than inspecting the SQL text. It is a typed hook; treating it as a string is how it breaks.
4. **A hook that mutates the model surprises the caller.** After `Insert`, the struct has been modified in place. Document it, or do the work in the calling code where it is visible.
5. **Errors from hooks abort the statement** and surface through the caller's error path — return them, never swallow them.

**Practice claim**: keep hooks small and declarative (timestamps, defaults, one invariant). When a hook starts needing lookups or branching on business state, the logic belongs in the service that owns the decision, where it can be tested without a database.

## Soft delete

```go
type User struct {
    bun.BaseModel `bun:"table:users,alias:u"`

    ID        int64     `bun:"id,pk,autoincrement"`
    Email     string    `bun:"email,unique,notnull"`
    DeletedAt time.Time `bun:"deleted_at,soft_delete,nullzero"`
}
```

Two behaviours come with the tag, and both are implicit:

```sql
-- a read: deleted rows disappear without you writing a predicate
SELECT * FROM users WHERE deleted_at IS NULL

-- a delete: no row is removed
UPDATE users SET deleted_at = current_timestamp WHERE id = 123
```

What follows from that:

- **`Delete` means "hide", not "remove".** Code that needs real removal must say so (a raw statement, or a model without the tag). Do not let the name mislead a reader — and do not write a cascade in Go expecting it to fire.
- **Reading deleted rows requires an explicit opt-out.** There is no accidental path back to them.
- **The unique-index trap.** A plain `UNIQUE (email)` index still counts soft-deleted rows: after deleting `a@example.com`, re-creating it fails with a unique violation. Options: a partial index (`UNIQUE (email) WHERE deleted_at IS NULL`), a composite key including the deletion column, or anonymising the unique value on delete. Choose deliberately — this is a schema decision, not a Bun one.
- **`RowsAffected` still works** for a not-found check (it counts updated rows), which is what most callers are actually asking.
- **Restoring is a normal update** (set `deleted_at` back to `NULL`); it is not a special operation, but it does re-enter every unique index.

**Practice claim**: soft delete is a product decision that leaks into every query, index and report. If only a handful of tables need history, prefer an explicit `archived_at`/`deleted_at` column with ordinary queries over a tag that silently filters — implicit behaviour that every future reader must remember is a cost paid on every query, forever.

## Verifying soft delete

```go
// the row is invisible through the model
var users []User
require.NoError(t, db.NewSelect().Model(&users).Where("email = ?", email).Scan(ctx))
assert.Empty(t, users)

// but it still exists
var raw int
require.NoError(t, db.NewRaw("SELECT COUNT(*) FROM users WHERE email = ?", email).Scan(ctx, &raw))
assert.Equal(t, 1, raw)
```

Assert both halves. A test that only checks "the query returned nothing" passes equally well when the insert never happened.

## Checklist

- [ ] Hook interfaces asserted (`var _ bun.BeforeAppendModelHook = (*User)(nil)`)
- [ ] Hooks limited to row-local invariants; no lookups, no business branching
- [ ] Bulk paths set per-row values explicitly — the entity hooks run once per statement on a zero value
- [ ] Soft delete understood as update + implicit filter; hard delete explicit
- [ ] Unique indexes account for soft-deleted rows (partial index or composite key)
- [ ] Tests assert both "invisible through the model" and "still present in the table"
