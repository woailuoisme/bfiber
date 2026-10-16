---
name: bun-orm
description: Use when writing, changing or reviewing Go code that reaches SQL through Bun ORM (github.com/uptrace/bun) — model and struct-tag design, relations and eager loading, query building, bulk writes and upserts, transactions and context propagation, hooks, soft delete, connection pooling, or migrations. Triggers: "Bun model", "bun.NewDB", "NewSelect", "NewInsert", "Relation(", "RunInTx", "bulk insert", "upsert", "ON CONFLICT", "soft delete", "Bun migration", "N+1", "eager load", "ScanAndCount", "bun.IDB", "pgdialect".
allowed-tools: Bash(bash:*)
---

# Bun ORM — Industry-Grade Practices

Produce and change Bun-backed persistence so the result is type-safe, injection-free, transactionally honest, and cheap to operate. This skill is a **generator first**: the rules below decide what to write, the `references/` files carry the depth, and `scripts/audit.sh` mechanically catches the regressions a reader would otherwise miss.

Verified against **Bun v1.3.0** with `pgdialect` and `pgx`. Bun's API surface is wide and SQL-first: most "ORM problems" here are really "the query you actually sent" problems, so the habit worth building is reading the generated SQL, not memorising builders.

## Resource Routing

| Resource | Read it when |
|---|---|
| `references/models-and-relations.md` | Defining tables, tags, nullability, or declaring/loading relations |
| `references/querying.md` | Building selects, filtering, pagination, subqueries, CTEs, row locks |
| `references/writes-and-bulk.md` | Insert/update/delete, bulk operations, upserts, `Returning` |
| `references/transactions.md` | Any multi-statement write, unit of work, context propagation, locking |
| `references/hooks-and-soft-delete.md` | Model hooks, timestamps, soft delete semantics and its interaction with unique indexes |
| `references/migrations-and-performance.md` | Connection pool, query hooks/observability, migrations, N+1 and bulk sizing |
| `scripts/audit.sh` | Before finishing any persistence change — run it on the touched packages |

## Decision rules

**Models and tags**
1. Declare the table and alias explicitly (`bun.BaseModel` with `table:…,alias:…`). Inferred names are fine until a join or a raw expression needs the alias; then you need it everywhere.
2. Tag the primary key (`pk`, and `autoincrement` when the database generates it). An implicit PK is a guess that each dialect resolves differently.
3. Push constraints into the tag (`notnull`, `unique`, `default:…`) so the struct and the migration agree. The tag is the contract; the database is the authority — a `unique` tag without the matching index is a lie.
4. Use `nullzero` for "empty means absent" columns. Use a pointer when "absent" and "zero" must stay distinguishable — a non-pointer `string`/`int` cannot express "not provided".
5. Timestamps get `nullzero` plus a database default (`default:current_timestamp`), so the value exists even for rows written outside your code.
6. Keep the model a data shape. If it starts making decisions or carrying transport concerns, the layer is wrong, not the tag.

**Relations**
7. Declare only relations the code actually loads, and load them with `Relation(name)` — an unused relation tag is an unverified claim about join conditions.
8. A polymorphic table needs its discriminator in the join. `rel:has-many,join:id=model_id` on a table that also has a type column will happily load rows belonging to other types.
9. Use the `Relation` callback to filter and order related rows **in SQL**, not in Go after loading everything.
10. Fetch children for a set of parents with one `Relation()` call, never with a query inside a loop (N+1).

**Querying**
11. Bind values with `?`. Bun escapes identifiers; it does not rescue a formatted string.
12. Qualify columns with the model alias (`u.id`) as soon as a relation, join or subquery is involved, or "column is ambiguous" becomes your problem at runtime.
13. `ScanAndCount` for paginated reads: its count query drops `ORDER BY` and `LIMIT/OFFSET`, so `total` is the full result size rather than the page size.
14. Prefer `WhereGroup`, `WhereIn`, `WherePK`, `Exists` over assembling `IN (…)` or `(a OR b)` strings; `WhereGroup` is what makes an OR-ed unit survive an outer AND.
15. Reuse a shared fragment with `Apply()` so the list query and the count query cannot drift apart.
16. Subqueries and CTEs are values: pass the `*bun.SelectQuery` as a parameter (`Where("id IN (?)", subq)`, `With("top", subq)`).
17. `For("UPDATE")` when the row is read and then written inside the same transaction; without it, two concurrent readers overwrite each other.

**Writes**
18. One statement per intent: bulk insert a slice, `On("CONFLICT … DO UPDATE").Set(...)` for an upsert, `Returning("id")` to read generated values back.
19. Batch large bulk inserts (order of a thousand rows per statement) to bound statement size and memory; one enormous `INSERT` is slower and harder to retry than several.
20. A `Delete` without a `Where` empties the table — and on a soft-deleting model it is not even a `DELETE`. Say which one you mean.

**Transactions**
21. `RunInTx` is the default unit of work. The callback receives a context that carries the transaction; anything that takes a `bun.IDB` and resolves it from the context joins that transaction.
22. Never issue a statement on a captured root handle from inside a transaction: it runs outside the transaction and survives the rollback. This is the single most common way a "transactional" write half-commits.
23. Nested units of work reuse the outer transaction rather than opening a second one; a callback that must run only after the real commit has to be registered for that, not executed in the callback body.
24. Irreversible side effects (files, HTTP calls, queue publishes) belong **after** the commit; inside the callback a rollback leaves the side effect behind with no row to justify it.
25. Manual `BeginTx` needs `defer tx.Rollback()` on the line after it. If you are writing that line, ask whether `RunInTx` would do.

**Hooks, soft delete, performance**
26. Use `BeforeAppendModel` for last-mile field work (timestamps, normalisation) and remember entity hooks and bulk operations are not the same path — do not assume a hook ran per row in a bulk write.
27. Soft delete is implicit filtering: reads exclude deleted rows automatically, and a `Delete` becomes an `UPDATE`. Reading deleted rows, or truly removing one, has to be explicit. A plain `unique` index that ignores deleted rows will block re-creating a deleted record.
28. Tune the pool (`SetMaxOpenConns`, `SetMaxIdleConns`, `SetConnMaxLifetime`). The default has no open-connection ceiling, which is how one service exhausts its database.
29. Add a query hook (`bunotel`, `bundebug` in development) so slow statements are visible, and make sure the hook does not log bound arguments on a path that carries secrets.
30. Migrations run as their own command, not on application start: every replica racing the same migration is a lock contention bug waiting for a deploy.

## Workflow

1. Locate the change: model/tag, relation, query, write, transaction, or migration. Read the matching reference before writing code.
2. Write it with the rules above. When a query gets complex, print the SQL (query hook or `db.NewSelect().String()`) and read it — that is the artefact that runs.
3. Run `bash scripts/audit.sh <packages…>` on what you touched and fix every FAIL. WARN lines are judgement calls; read the reason.
4. Verify the behaviour, not the shape: assert that a soft-deleted row is excluded, that a rollback left nothing behind, that the paginated total is the full count, that the upsert updated instead of inserted. A test that only checks "no error" proves nothing about SQL.
5. If an API does not behave as this skill claims, re-verify against Context7 (below) and treat this skill as stale for that area.

## Re-verification pointers

- Bun guides (models, relations, hooks, soft delete, pooling, monitoring): Context7 library `/websites/bun_uptrace_dev`
- Bun source-level questions (what a builder really emits): Context7 library `/uptrace/bun`
- Pin of record for this skill: **bun v1.3.0**, `bun/dialect/pgdialect` v1.3.0, `jackc/pgx/v5` v5.11.0

Claims that are engineering judgement rather than something the Bun docs state are marked **practice claim** in the references. Where one conflicts with your project's convention, the project wins.

## Related skills

- **`fiber-v3`** — the request half: handlers must not own SQL, and a transaction reaches the persistence layer through `context.Context`
- **`go-code-review`** (user-level skill under `~/.codebuddy/skills/`) — general Go style, error and naming review to run alongside this one

Referenced by name rather than by relative link on purpose: a relative link only resolves inside one skills root, and this skill's root differs per installation (user-level versus a project's `.agents/skills/`). A name cannot dangle.
