# Transactions and context propagation

## `RunInTx` is the default unit of work

```go
err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
    if _, err := tx.NewInsert().Model(&order).Exec(ctx); err != nil {
        return err
    }
    if _, err := tx.NewUpdate().Model(&stock).
        Set("quantity = quantity - ?", order.Quantity).
        Where("id = ?", order.StockID).
        Exec(ctx); err != nil {
        return err
    }
    return nil          // returning nil commits; a returned error rolls back
})
```

Returning an error is what rolls back. A `panic` inside the callback that is recovered outside leaves the transaction's fate to the driver's cleanup — do not use panics as control flow here.

## The context carries the transaction — use it

The callback hands you a `context.Context` that already holds the transaction. Anything that receives that context and resolves its handle from it joins the same transaction:

```go
type Store struct {
    db bun.IDB                // IDB, not *bun.DB: *DB, *Conn and *Tx all satisfy it
}

func (s *Store) handle(ctx context.Context) bun.IDB {
    if tx, ok := database.DBFromContext(ctx); ok {
        return tx
    }
    return s.db
}

func (s *Store) Save(ctx context.Context, u *User) error {
    _, err := s.handle(ctx).NewInsert().Model(u).Exec(ctx)
    return err
}
```

Two properties follow, and they are the reason to write it this way:

- a caller inside a transaction gets the transaction **without** threading `tx` through every function signature;
- a caller outside one gets the pool — the same code works in both positions, so a function never has to know which it is in.

**Practice claim**: the seam (a context-carried handle plus a resolver) is worth adding as soon as two packages write to the same tables. Without it, "transactional" is a property of the call site, and the first caller that forgets silently half-commits.

## The trap: a statement that escapes the transaction

```go
// WRONG: s.db (or a *bun.DB captured at construction) is the root pool
err := db.RunInTx(ctx, parent, func(ctx context.Context, tx bun.Tx) error {
    if err := s.Save(ctx, order); err != nil {   // joins the tx: good
        return err
    }
    _, err := s.root.NewInsert().Model(&audit).Exec(ctx)   // runs OUTSIDE the tx
    return err                                             // survives a rollback
})
```

A statement issued on the root handle runs on a different connection, in its own implicit transaction, and **is not rolled back**. A write you believed was atomic half-commits, and the failure appears later as data with no explanation.

Rules that prevent it:

1. Store `bun.IDB`, never `*bun.DB`, in anything that writes on behalf of a caller. The type is the guard: `*bun.DB` invites a statement that cannot join a transaction, and `bun.IDB` accepts the `bun.Tx` too.
2. Resolve the handle from the context at the point of use, not at construction.
3. The only code that may legitimately hold the root handle is the composition root that owns the pool, and the code that opens transactions.

## Nested units of work

Calling `RunInTx` inside a transaction reuses the outer one rather than opening a second. That is the behaviour you want for composability — the alternative is a savepoint per call. It also means a nested call **cannot** commit independently: its failure still takes down the outer unit, and its success does not persist until the outer commit.

If a callback must run exactly once, at the real commit boundary — deleting a file, publishing an event — register it for after the commit rather than executing it in the callback body. A side effect that has already happened cannot be undone by a rollback:

```go
err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
    if _, err := tx.NewInsert().Model(record).Exec(ctx); err != nil {
        return err
    }
    OnCommit(ctx, func() { _ = storage.Delete(oldFile) })   // only if this layer commits
    return nil
})
```

**Practice claim**: an `OnCommit`-style seam (a context-carried hook list drained by the layer that opens the transaction) is the honest way to model "after the commit". The alternative — checking whether a transaction is already active and skipping the side effect — quietly turns "no dangling record" into "leaked file".

## Manual transactions

```go
tx, err := db.BeginTx(ctx, nil)
if err != nil {
    return err
}
defer func() { _ = tx.Rollback() }()   // no-op after a successful Commit

// ... use tx ...
return tx.Commit()
```

`defer tx.Rollback()` on the line after `BeginTx` is not optional: every early return that forgets it pins a connection until the pool's lifetime settings reclaim it. If you find yourself needing `BeginTx` for flow control (a decision to commit or roll back based on a result), prefer `RunInTx` and return the error.

## Row locks and isolation

- Read-modify-write inside a transaction needs `For("UPDATE")` on the read, or two writers will read the same value.
- `RunInTx`/`BeginTx` accept `*sql.TxOptions`; set the isolation level deliberately when the default of your database's configuration is not what the operation assumes.
- A deadlock or serialization failure is a normal outcome at higher isolation levels. Retry the **whole unit of work**, not the individual statement — which requires the callback to be free of side effects that already happened.

## Verifying a transaction

The test that matters is the rollback test:

```go
err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
    if err := writeRow(ctx); err != nil { return err }
    return errors.New("force rollback")
})
require.Error(t, err)

// the row must not exist: query it on a fresh handle
```

Add the same shape for the side effect (`OnCommit` must not have run) and for the escaped-statement case (a helper that writes on the root handle must be visible after the rollback — which is the bug, made observable).

## Checklist

- [ ] Multi-statement writes go through `RunInTx`; the callback returns errors instead of panicking
- [ ] Writers hold `bun.IDB` and resolve from the context at the point of use
- [ ] No statement inside a transaction is issued on a captured root handle
- [ ] Irreversible side effects run after the commit, not in the callback body
- [ ] `BeginTx` (if used) is immediately followed by `defer tx.Rollback()`
- [ ] Read-modify-write locks the row with `For("UPDATE")`
- [ ] A rollback test asserts nothing was written and no side effect ran
