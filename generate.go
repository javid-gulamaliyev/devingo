package devin

// Generation is deterministic and uses only checked-in contracts and Go's
// standard library. Run from the module root with: go generate .
//
//go:generate go run ./internal/cmd/mergespec
//go:generate go run ./internal/cmd/genmodels -spec spec/combined/v3.json -out models.gen.go -package devin
//go:generate go run ./internal/cmd/genservices -spec spec/combined/v3.json -outdir . -package devin
//go:generate go run ./internal/cmd/genservices -spec spec/combined/v1.json -outdir v1 -package v1 -legacy v1
//go:generate go run ./internal/cmd/genmodels -spec spec/combined/v1.json -out v1/models.gen.go -package v1 -support-import devingo
//go:generate go run ./internal/cmd/genservices -spec spec/combined/v2.json -outdir v2 -package v2 -legacy v2
//go:generate go run ./internal/cmd/genmodels -spec spec/combined/v2.json -out v2/models.gen.go -package v2 -support-import devingo
//go:generate go run ./internal/cmd/genindex
