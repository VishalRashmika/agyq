<!-- Example queue. Anything before the first "# #N" heading is ignored. -->

# #1 Scaffold the project
Create a Go module named `demo` with a `main.go` that prints "hello".
Done when `go build ./...` succeeds.

# #2 Add a greeting package
Create package `greet` with `func Hello(name string) string` and a table-driven test.

Example of a fenced block that must NOT be parsed as a heading:

```markdown
# #9 this is just text inside a code fence
```

Done when `go test ./...` passes.

# #3
A prompt with no title text. Add a README section describing the `greet` package.

# #4: Colon-style title
Wire `greet.Hello` into `main.go` and accept a `-name` flag.

# #5 - Dash-style title
Add a GitHub Actions workflow that runs `go vet` and `go test`.
