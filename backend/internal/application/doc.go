// Package application coordinates authenticated requests, durable task state,
// execution governance, providers and persistence. Business value types and pure
// validation live in internal/modules; reusable infrastructure lives in platform.
//
// The existing file/PostgreSQL transaction implementation remains here during
// migration. Its acceptance, reservation, enqueue and fenced-completion boundary
// must be preserved when extracting additional repositories or adapters.
package application
