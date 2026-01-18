// Package kafka provides a real-time Kafka-based interface for PostgreSQL,
// similar to how PostgREST exposes PostgreSQL over HTTP.
//
// Kafka topic naming conventions:
// - Case-sensitive, no spaces
// - Valid chars: alphanumeric, `.`, `-`, `_`
// - Recommended max length: 249 bytes (to avoid potential issues)
// - Forward slash (`/`) can be used for logical separation but requires proper escaping
//
// pigo uses `[prefix].[schema_name].[table_name].[operation]` topic pattern to interact with PostgreSQL
//
// Operations:
// - create (or c): Insert operations
// - update (or u): Update operations
// - delete (or d): Delete operations
// - read (or r): Query operations
// - truncate (or t): Truncate operations
//
// Examples:
// - public.users.c          → Create user
// - inventory.products.u    → Update product
// - accounting.invoices.r   → Read invoices
//
// Payload: JSON
//
// Message Format:
// - Key: Unique identifier (e.g., primary key)
// - Value: JSON payload
// - Headers: Metadata including timestamp, operation type, etc.
//
// Partitioning Strategy:
// - Default: Hash partitioning based on primary key
// - Custom: Can be configured based on specific fields
//
// Query Parameters:
// [schema_name].[table_name].read.[field].[value]
// Example: public.users.r.id.123 → read by id 123
//
// Consumer Groups:
// - Use meaningful names: `[app_name].[purpose]`
// - Example: myapp.user_updates
//
// Configuration:
// - Replication Factor: Minimum 2 recommended for production
// - Number of Partitions: Based on throughput requirements
// - Retention: Configurable per topic
//
// Use 'prefix.pg' topic for system operations
//
// Note: Ensure proper ACLs are configured for topic access
package kafka
