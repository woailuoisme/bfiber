# Pool, observability, migrations and performance

## Connection pool

```go
func configurePool(db *bun.DB) {
    sqldb := db.DB

    sqldb.SetMaxOpenConns(25)                  // ceiling: the default is unlimited
    sqldb.SetMaxIdleConns(25)
    sqldb.SetConnMaxLifetime(5 * time.Minute)  // survives failovers, recycles TLS
    sqldb.SetConnMaxIdleTime(5 * time.Minute)
}
```

- **`SetMaxOpenConns` is the one that matters.** Left unset, the pool opens connections on demand until the database refuses them — a traffic spike turns into an outage for every service sharing that database.
- The ceiling is a budget across **all** replicas: 25 per instance × 10 instances = 250 connections against a server whose `max_connections` may be 100. Divide by replica count before choosing.
- `SetConnMaxLifetime` protects against a connection that outlives a failover or a rotated credential; keep it shorter than any infrastructure-level idle timeout.
- With pgx you may instead build the pool with `pgxpool` and hand it to Bun via `stdlib.OpenDBFromPool(pool)`. Pool tuning then lives in the pgx config, and `db.DB` is a wrapper — do not tune both.

## Observability

```go
db.AddQueryHook(bunotel.NewQueryHook(bunotel.WithDBName("orders")))     // traces + metrics
db.AddQueryHook(bundebug.NewQueryHook(bundebug.WithVerbose(true)))      // development only
```

- A query hook is how you find the statement that is actually slow. Without one, "the endpoint is slow" has no evidence attached.
- `bundebug` logs SQL **with arguments**. Never enable it in production on a path whose arguments carry credentials, tokens or personal data.
- Hooks cost something per statement; enable them deliberately rather than "just in case".
- **Practice claim**: tracing on the query hook plus an index review beats a slow-query log nobody reads — the trace tells you which request paid and which statement it was.

## N+1 and set-based thinking

The shape to recognise:

```go
// one query for the parents, then one per parent: N+1
for _, user := range users {
    var posts []Post
    db.NewSelect().Model(&posts).Where("author_id = ?", user.ID).Scan(ctx)
    user.Posts = posts
}
```

Replace it with a relation load (`Relation("Posts")`, see `models-and-relations.md`) or one `WhereIn` query over the collected ids. The rule of thumb: the number of statements a request issues should not grow with the number of rows it returns.

Other shapes worth fixing while you are there:

- a query inside a loop for a value that does not depend on the loop variable (hoist it);
- a count and a list fetched separately for the same predicate (`ScanAndCount` removes the second round trip and the drift);
- a bulk insert issued one row at a time (batch it, see `writes-and-bulk.md`);
- `SELECT *` semantics on a wide table where the caller needs three columns (`Column(...)` reduces bytes on the wire, and it makes the dependency explicit).

## Indexes follow the predicates

Write the predicate, then check the plan for the query that uses it:

```sql
EXPLAIN (ANALYZE, BUFFERS) SELECT ... WHERE tenant_id = $1 AND created_at < $2 ORDER BY created_at DESC LIMIT 20;
```

The index that serves a list endpoint is usually a composite one in the order (equality columns, then the range/sort column). Two mistakes that show up in every codebase:

1. an index on a column the query never filters alone;
2. an `ORDER BY` on a column that is not the last part of the index, forcing a sort of the whole result.

**Practice claim**: an index is part of the query's contract. Changing a list endpoint's filters without revisiting its index is how a fast endpoint becomes a slow one with no code change to explain it.

## Migrations

```go
var Migrations = migrate.NewMigrations()

func init() {
    Migrations.MustRegister(
        func(ctx context.Context, db *bun.DB) error { /* up */ return nil },
        func(ctx context.Context, db *bun.DB) error { /* down */ return nil },
    )
}

migrator := migrate.NewMigrator(db, Migrations)
if err := migrator.Lock(ctx); err != nil {
    return err
}
defer func() { _ = migrator.Unlock(ctx) }()

group, err := migrator.Migrate(ctx)
```

Rules:

1. **Run migrations as their own command, not from application startup.** Every replica racing the same migration is lock contention with a side of partial schema. The advisory lock protects correctness, not your deploy time.
2. **Every change has an up and a down.** A down that does not exist is a rollback you cannot perform; a down that drops a column is a rollback that loses data — say which one you are writing.
3. **Never edit an applied migration.** Schema changes go forward; editing history produces two databases that disagree while both report "up to date".
4. **Additive first.** Add the column, deploy code that writes both, then remove the old one — a migration that both changes shape and breaks the running code is a downtime request.
5. **Indexes belong in migrations**, including the partial/unique ones that soft delete needs. A constraint that exists only in a struct tag does not exist.
6. Keep migration files deterministic: no `time.Now()`, no reads of the application's environment, no data derived from the running process.

## Checklist

- [ ] `SetMaxOpenConns` set, sized against the database's limit divided by replicas
- [ ] `SetConnMaxLifetime` shorter than any infrastructure idle timeout
- [ ] Query hook enabled for traces/metrics; verbose SQL logging only outside production
- [ ] No statement count that grows with the number of returned rows
- [ ] List endpoints' predicates covered by a composite index in the right column order
- [ ] Migrations run as their own command, locked, with an up **and** a down
- [ ] Schema changes additive-first; no applied migration edited
