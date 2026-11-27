# Seeded review test: answer key

`store.go` has 10 planted problems and 6 decoys: correct code that looks
suspicious. Run review-bot against the PR and score it.

- **Caught:** a comment on (or within 2 lines of) a planted problem, with the
  right category. Severity is a bonus.
- **False positive:** any comment on a decoy, or anywhere else.

## Planted problems

| # | Line | Category | Expect | Problem |
|---|---|---|---|---|
| B1 | 38 | bugs | high | `i <= len(items)` reads one past the end: `Total([]Item{{"a", 1}})` panics with index out of range |
| B2 | 50 | bugs | high/medium | `Average(nil)` divides by zero and panics |
| B3 | 67 | bugs | high | writing to a nil map: `CountNames([]string{"a"})` panics |
| B4 | 82 | bugs | high | goroutines update `total` with no lock: a data race, totals come out wrong |
| B5 | 91 | bugs | high/medium | error from `os.Open` ignored: a missing file gives a nil `f`, and `f.Read` panics |
| S1 | 110 | security | critical | SQL injection: `?name=x' OR '1'='1` returns every user |
| S2 | 133 | security | critical/high | path traversal: `?file=../../etc/passwd` reads outside `baseDir` |
| S3 | 155 | security | critical | command injection: `img=a.png; rm -rf ~` runs in a shell |
| S4 | 16, 165 | security | high | hard-coded admin password in source, and the comparison that uses it |
| S5 | 175 | security | high | TLS certificate checks disabled for the payment client |

## Decoys (should get no comments)

| # | Line | Why it's fine |
|---|---|---|
| D1 | 54–62 | `SafeAverage` checks for an empty list before dividing |
| D2 | 99–105 | `ReadConfig` returns its error |
| D3 | 122 | `GetOrder` uses a parameterized query |
| D4 | 142–150 | `Invoice` cleans the path and checks it stays inside `invoices/` |
| D5 | 180–188 | `CachedCount` holds the mutex for the whole update |
| D6 | 191–202 | `Field`'s index arithmetic is correct (`rest` starts after the delimiter) |

## Score

- Recall: caught / 10
- False positives: count
