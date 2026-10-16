# Writes, bulk operations and upserts

## Insert

```go
user := &User{Email: "a@example.com", Name: "Alice"}
_, err := db.NewInsert().Model(user).Exec(ctx)          // user.ID populated after

// returning generated columns
var ids []int64
db.NewInsert().Model(&users).Returning("id").Exec(ctx, &ids)
```

`Model` accepts a value (one row) or a slice/pointer-to-slice (many rows). Bulk insert of a slice is **one statement**, which is what you want up to a point.

## Bulk sizing

```go
const batchSize = 1000
for i := 0; i < len(users); i += batchSize {
    end := min(i+batchSize, len(users))
    if _, err := db.NewInsert().Model(&users[i:end]).Exec(ctx); err != nil {
        return err
    }
}
```

Why batch rather than sending everything:

- a statement with tens of thousands of placeholders can exceed the driver's parameter limit and will certainly exceed what you can retry cheaply;
- memory for the query text grows with the row count;
- a failure costs one batch rather than the whole load.

**Practice claim**: an order of a thousand rows per statement is a reasonable default; measure it, because the right number depends on row width and on how the database plans the insert.

## Upsert

```go
_, err := db.NewInsert().
    Model(user).
    On("CONFLICT (email) DO UPDATE").
    Set("name = EXCLUDED.name").
    Set("updated_at = current_timestamp").
    Exec(ctx)
```

- The conflict target must match a real unique index, or the statement fails at runtime rather than at review time.
- `EXCLUDED` refers to the row you tried to insert; saying `DO NOTHING` instead is a different semantic (an update that silently does not happen is often a bug, not a feature).
- **Practice claim**: an upsert that sets every column will clobber fields the caller did not intend to change. Enumerate the columns.

## Update

```go
_, err := db.NewUpdate().
    Model(&user).
    Column("name", "updated_at").              // restrict the columns explicitly
    Set("name = ?", name).
    Set("updated_at = ?", time.Now().UTC()).
    Where("id = ?", user.ID).
    Exec(ctx)
```

- `.Set("col = ?", value)` binds. `.Set("col = '" + value + "'")` is injection.
- `Omit` is the inverse of `Column` and is easy to get wrong once columns are added; prefer naming what you write.
- An update with no `Where` updates every row. There is no undo and no warning — the audit script flags the shape for this reason.

## Delete, and what soft delete changes

```go
_, err := db.NewDelete().Model((*User)(nil)).Where("id = ?", id).Exec(ctx)
```

With `soft_delete` on the model this is not a `DELETE`: Bun rewrites it into an `UPDATE … SET deleted_at = current_timestamp`. Consequences worth stating before you rely on either behaviour:

- `RowsAffected` now means "rows updated", which is still what you want for a not-found check.
- A genuinely hard delete requires saying so explicitly (raw statement with `db.Exec`, or a model without the soft-delete field). Do not assume `Delete` means removal.
- A cascade declared in the database still fires on a hard delete only; a soft delete leaves children pointing at a row that reads as absent.

## One row, one statement, one intent

```go
// bulk update of many rows with the same value
_, err := db.NewUpdate().Model((*User)(nil)).
    Set("status = ?", StatusInactive).
    Where("last_login_at < ?", cutoff).
    Exec(ctx)
```

Two anti-patterns to avoid:

1. **Read-modify-write per row in Go** where a single `UPDATE … WHERE` expresses the same thing. The set-based statement is one round trip and is atomic per statement; the loop is neither.
2. **A loop of single inserts** where a bulk insert fits. Same reasoning, plus transaction overhead multiplied by row count.

## Verifying a write

Assert the effect on state, not the absence of an error:

- an insert → the row exists with the generated id and defaults applied;
- an upsert → the row count is unchanged and the updated column has the new value;
- a soft delete → the row still exists (raw query) but is invisible through the model;
- a bulk insert → the count went up by exactly the batch size, including the last partial batch.

## Checklist

- [ ] Bulk inserts batched; the last partial batch included
- [ ] Upsert conflict target matches a real unique index; columns enumerated
- [ ] Updates name their columns and always carry a `Where`
- [ ] Deletes carry a `Where`; soft-delete semantics understood (updated, not removed)
- [ ] No per-row read-modify-write where one set-based statement would do
- [ ] Write tests assert resulting state, not just "no error"
