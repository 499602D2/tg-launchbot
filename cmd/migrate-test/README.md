# Database Migration Test Tool

This tool helps test GORM auto-migrations on a copy of an existing database without affecting the original.

## Usage

```bash
# Basic usage
go run main.go -source path/to/database.db

# With options
go run main.go -source data/launchbot_3.2.17.db -verbose
go run main.go -source data/launchbot.db -dry-run
go run main.go -source data/launchbot.db -keep=false  # Remove test DB after
```

## Options

- `-source`: Path to the source database file to test migration on (required)
- `-verbose`: Enable verbose GORM logging to see all SQL queries
- `-dry-run`: Show what would be migrated without actually doing it
- `-keep`: Keep the test database after migration (default: true)

## What it does

1. Creates a copy of the source database with `_migration_test` suffix
2. Analyzes the current schema
3. Runs GORM AutoMigrate for all models
4. Analyzes the schema after migration
5. Shows what changed (new tables, columns, etc.)
6. Verifies data integrity
7. Runs functionality tests to ensure queries still work

## Example Output

```
Creating test database copy: data/launchbot_3.2.17_migration_test.db

=== Analyzing Current Schema ===
Table: statistics (12 columns, 2 indexes)
Table: launches (122 columns, 3 indexes)
Table: users (23 columns, 3 indexes)

=== Running Auto-Migration ===
Migration completed successfully!

=== Schema Changes ===
✅ New column in users: blocked_keywords (TEXT)
✅ New column in users: allowed_keywords (TEXT)

=== Data Integrity Check ===
✓ Launches: 842
✓ Users: 5517
✓ Statistics records: 1

=== Functionality Tests ===
✓ Basic launch query
✓ User notification query (found 5 users)
✓ Keyword fields query (found 0 users with filters)
✓ Statistics query

Tests passed: 4/4
```