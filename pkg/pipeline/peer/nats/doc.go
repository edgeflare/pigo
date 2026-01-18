// Package nats provides a real-time NATS-based interface for PostgreSQL,
// similar to how PostgREST exposes PostgreSQL over HTTP.
//
// NATS subject (aka topic) patterns:
//   - Case-sensitive, dot-separated, no spaces
//   - Valid chars: alphanumeric, `-` or `_`
//   - Max length: 255 bytes
//
// pigo uses `any.nested.prefix.schema_name.table_name.operation` topic to interact with PostgreSQL
//
// Operations:
//   - create (or c): Insert operations
//   - update (or u): Update operations
//   - delete (or d): Delete operations
//   - read   (or r): Query operations
//   - truncate (or t): Truncate operations
//
// Examples:
//   - public.users.c           → Create user
//   - inventory.products.u     → Update product
//   - accounting.invoices.r    → Read invoices
//
// Payload: JSON
//
// Query Parameters:
//
//		schema_name.table_name.read.field.value
//		Example: public.users.r.id.123          → read by id 123
//
//	 Use '$PG.' prefix for system operations
package nats
