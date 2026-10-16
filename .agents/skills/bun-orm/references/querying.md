# Querying

## Bind, never format

```go
// correct: the value travels as a parameter
db.NewSelect().Model(&users).Where("u.email = ?", email)

// wrong on every reading: the value is part of the SQL text
db.NewSelect().Model(&users).Where("u.email = '" + email + "'")
```

Bun escapes the identifiers you write and binds the values you pass as `?`. It cannot protect a string you assembled yourself. This includes `fmt.Sprintf` around a predicate, and `Set("name = '" + name + "'")` in an update.

Identifiers are a separate question: a column name coming from user input must be validated against an allow-list before it reaches `Order`/`Column`, because an identifier cannot be bound.

## The builder pieces that replace hand-written SQL

```go
q := db.NewSelect().Model(&users).
    Where("u.status = ?", status).
    WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {   // OR-ed as a unit
        return q.Where("u.name ILIKE ?", pattern).
            WhereOr("u.email ILIKE ?", pattern)
    }).
    WhereIn("u.role IN (?)", roles)

if ids, ok := filter.IDs(); ok {
    q = q.WhereIn("u.id IN (?)", ids)
}
```

- `WhereGroup` exists because a bare `Where("a").WhereOr("b")` bound to the outer query lets the OR swallow your other conditions.
- `WhereIn` with a slice generates the right number of placeholders; building `IN (?)` by joining strings does not.
- `Exists(subq)` / `Where("id IN (?)", subq)` express set membership in the database instead of fetching ids into Go and passing them back.
- `WherePK` when you already hold the model — shorter and less error-prone than repeating the key columns.

## Qualified columns

Once more than one table is in play, an unqualified column is ambiguous:

```go
q := db.NewSelect().Model(&users).
    Join("JOIN accounts AS a ON a.user_id = u.id").
    Where("a.provider = ?", provider)   // qualified: no ambiguity, no surprise
```

**Practice claim**: qualify every column in a predicate, even when only one table is present today. The predicate you write now outlives the query it was written for.

## Pagination that counts the right thing

```go
var items []User
q := db.NewSelect().Model(&items).Where("u.status = ?", status).Order("u.created_at DESC")

total, err := pagination.Apply(q).ScanAndCount(ctx)
```

`ScanAndCount` runs the count on the same query with `ORDER BY` and `LIMIT/OFFSET` removed, so `total` is the size of the full result set rather than the page. Two consequences:

1. Do not compute the total with a separate `Count()` on a hand-rebuilt query — that is exactly how the list and the count drift apart after the next filter is added to only one of them.
2. Pass the **same** query value to the counter; if you must build the predicate separately, share it through `Apply()`:

```go
applyFilter := func(q *bun.SelectQuery) *bun.SelectQuery {
    return q.Where("u.status = ?", status)
}
q := applyFilter(db.NewSelect().Model(&items))
total, _ := pg.Apply(q).ScanAndCount(ctx)
```

## Subqueries and CTEs

A subquery is a query value, not a string:

```go
// WHERE id IN (subquery)
active := db.NewSelect().Model((*User)(nil)).Column("id").Where("u.status = ?", "active")
db.NewSelect().Model(&orders).Where("o.user_id IN (?)", active)

// computed column from a subquery
db.NewSelect().Model(&users).
    ColumnExpr("(SELECT COUNT(*) FROM posts WHERE author_id = u.id) AS post_count")

// CTE: With(name, query), then reference the name in the outer query
regional := db.NewSelect().ColumnExpr("region").ColumnExpr("SUM(amount) AS total_sales").
    TableExpr("orders").GroupExpr("region")
db.NewSelect().
    With("regional_sales", regional).
    ColumnExpr("region").
    TableExpr("regional_sales").
    Where("total_sales > (SELECT SUM(total_sales) / 10 FROM regional_sales)")
```

## Row locks

```go
err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
    var account Account
    if err := tx.NewSelect().Model(&account).
        Where("id = ?", id).
        For("UPDATE").                       // lock this row until commit
        Scan(ctx); err != nil {
        return err
    }
    _, err := tx.NewUpdate().Model(&account).Column("balance").
        Set("balance = ?", account.Balance-delta).
        Where("id = ?", id).Exec(ctx)
    return err
})
```

Without `For("UPDATE")` two concurrent read-modify-write sequences read the same value and one update is silently lost. The lock is only meaningful inside a transaction, and the row must be locked **before** it is read, not after.

## Reading the SQL

The generated SQL is the artefact that runs; read it when a query surprises you:

- `q.String()` for a quick look at the statement and its arguments.
- A query hook for the real statement as the driver saw it (see `migrations-and-performance.md`).

**Practice claim**: when a query returns fewer rows than expected, print the SQL before changing the Go. Nine times in ten the predicate is not what the author thought.

## Checklist

- [ ] Every value bound with `?`; no formatted SQL, no concatenated predicate
- [ ] Column identifiers coming from input validated against an allow-list
- [ ] OR-ed conditions inside `WhereGroup`, not loose `WhereOr` on the outer query
- [ ] Pagination total from `ScanAndCount` on the same query
- [ ] Subqueries/CTEs passed as query values
- [ ] Read-modify-write inside a transaction uses `For("UPDATE")`
