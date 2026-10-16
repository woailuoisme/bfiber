# Models, tags and relations

## Declare the table, the alias and the key

```go
type User struct {
    bun.BaseModel `bun:"table:users,alias:u"`

    ID        int64     `bun:"id,pk,autoincrement"`
    Email     string    `bun:"email,unique,notnull"`
    Name      string    `bun:"name,nullzero,notnull,default:'Anonymous'"`
    Score     int       `bun:"score,default:0"`
    UUID      string    `bun:"uuid,type:uuid,default:gen_random_uuid()"`
    CreatedAt time.Time `bun:"created_at,nullzero,notnull,default:current_timestamp"`
    UpdatedAt time.Time `bun:"updated_at,nullzero,notnull,default:current_timestamp"`
    DeletedAt time.Time `bun:"deleted_at,soft_delete,nullzero"`
}
```

Without `BaseModel`, Bun infers the table from the struct name (`ArticleCategory` → `article_categories`) and the alias from its singular (`article_category`). That inference is convenient and becomes a liability the moment a query has to reference the alias: qualifying a column with a name you did not choose couples your SQL to a convention. **Practice claim**: declare `table` and `alias` on every model that participates in a join, a subquery or a hand-written predicate.

## Tag rules worth internalising

| Tag | Meaning | Why it matters |
|---|---|---|
| `pk` | primary key | Multiple `pk` fields make a composite key |
| `autoincrement` | database generates it | Omit when your code assigns the id |
| `notnull` | NOT NULL | Pairs with a migration, not a substitute for one |
| `unique` | unique | Must match an actual index; see the soft-delete trap below |
| `default:…` | database default | Accepts expressions (`current_timestamp`, `gen_random_uuid()`) |
| `nullzero` | empty value → NULL | The switch that decides whether `""`/`0` are written at all |
| `type:…` | explicit column type | Required for `uuid`, `jsonb`, arrays and enums |
| `soft_delete` | delete becomes update | Implicit filtering on every read (see the soft-delete reference) |
| `scanonly` | read-only field | Lets you select a joined/aggregated column without writing it |

Three rules that prevent most model bugs:

1. **`nullzero` is a decision, not a default.** `Name string \`bun:"name"\`` writes `""` for a missing name; add `nullzero` and the same field writes `NULL`. If "absent" and "empty" are different states for your clients, only `NULL` can express it — and that requires `nullzero` on the field or a pointer instead of a value.
2. **A pointer means "may be absent".** Do not use pointers for columns that are always present just to dodge `nullzero`; each pointer is an allocation and a nil check for every reader.
3. **The tag is the struct's claim about the schema.** A `unique` tag without the index does not enforce anything, and an index without the tag means Bun-generated DDL disagrees with reality. Keep both, and let migrations own the index.

## Relations

```go
type User struct {
    bun.BaseModel `bun:"table:users,alias:u"`

    ID    int64  `bun:"id,pk,autoincrement"`
    Posts []Post `bun:"rel:has-many,join:id=author_id"`
}

type Post struct {
    bun.BaseModel `bun:"table:posts,alias:p"`

    ID       int64 `bun:"id,pk,autoincrement"`
    AuthorID int64 `bun:"author_id,notnull"`
    Author   *User `bun:"rel:belongs-to,join:author_id=id"`
    Status   string `bun:"status,notnull"`
}
```

Many-to-many needs the join model declared with both halves:

```go
type UserRole struct {
    bun.BaseModel `bun:"table:user_roles"`

    UserID int64 `bun:"user_id,pk"`
    RoleID int64 `bun:"role_id,pk"`
    User   *User `bun:"rel:belongs-to,join:user_id=id"`
    Role   *Role `bun:"rel:belongs-to,join:role_id=id"`
}

// on User:
Roles []Role `bun:"m2m:user_roles,join:User=Role"`
```

### Loading

```go
var users []User
err := db.NewSelect().
    Model(&users).
    Relation("Posts", func(q *bun.SelectQuery) *bun.SelectQuery {
        return q.Where("status = ?", "published").Order("created_at DESC")
    }).
    Scan(ctx)
```

- The callback is applied **in the related query**, so filtering and ordering happen in the database rather than in memory after loading rows you will discard.
- `Relation` loads a set of parents in a bounded number of statements. Reaching for a query inside a loop over parents is the N+1 shape that `Relation` exists to remove.

### The polymorphic trap

A relation whose join omits the discriminator is wrong, not merely incomplete:

```go
// a media table with model_type + model_id: this join loads rows of EVERY type
Media []*Media `bun:"rel:has-many,join:id=model_id"`
```

Activating `Relation("Media")` here returns media that belongs to other model types whenever the ids collide. Either add the discriminator to the query through the callback (`q.Where("m.model_type = ? AND m.collection_name = ?", …)`), or keep the load in a function that owns that knowledge instead of exposing the relation.

**Practice claim**: if activating a relation requires every caller to add the same filter, the relation should not be exported as a tag at all — a declared-but-filter-required relation is a trap for the next reader.

## Checklist

- [ ] `BaseModel` declares `table` and `alias` for anything joined or qualified
- [ ] `pk` (and `autoincrement` where the database generates it) declared
- [ ] `nullzero`/pointer choice made per column, matching whether "absent" is expressible
- [ ] Timestamps carry `nullzero` + a database default
- [ ] Every declared relation is loaded somewhere; polymorphic joins carry their discriminator
- [ ] Unique/notnull claims have matching indexes in a migration
