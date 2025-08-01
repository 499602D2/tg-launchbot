package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	dbpkg "launchbot/db"
	"launchbot/stats"
	"launchbot/users"
)

var (
	sourceDB   = flag.String("source", "", "Source database file to test migration on")
	verbose    = flag.Bool("verbose", false, "Enable verbose GORM logging")
	dryRun     = flag.Bool("dry-run", false, "Show what would be migrated without actually doing it")
	keepTestDB = flag.Bool("keep", true, "Keep the test database after migration")
)

func main() {
	flag.Parse()

	if *sourceDB == "" {
		fmt.Println("Usage: go run main.go -source <database-file> [options]")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Verify source database exists
	if _, err := os.Stat(*sourceDB); os.IsNotExist(err) {
		log.Fatalf("Source database not found: %s", *sourceDB)
	}

	// Create test database path
	dir := filepath.Dir(*sourceDB)
	base := filepath.Base(*sourceDB)
	ext := filepath.Ext(base)
	name := base[:len(base)-len(ext)]
	testDB := filepath.Join(dir, fmt.Sprintf("%s_migration_test%s", name, ext))

	// Copy the database file
	fmt.Printf("Creating test database copy: %s\n", testDB)
	data, err := os.ReadFile(*sourceDB)
	if err != nil {
		log.Fatalf("Failed to read source database: %v", err)
	}

	err = os.WriteFile(testDB, data, 0644)
	if err != nil {
		log.Fatalf("Failed to write test database: %v", err)
	}

	// Clean up test database on exit unless keeping
	if !*keepTestDB {
		defer func() {
			os.Remove(testDB)
			fmt.Println("Test database removed")
		}()
	}

	// Configure GORM logger
	logLevel := logger.Error
	if *verbose {
		logLevel = logger.Info
	}

	// Open the test database
	gormDB, err := gorm.Open(sqlite.Open(testDB), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
	})
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// Get the underlying SQL database
	sqlDB, err := gormDB.DB()
	if err != nil {
		log.Fatalf("Failed to get SQL database: %v", err)
	}
	defer sqlDB.Close()

	// Analyze current schema
	fmt.Println("\n=== Analyzing Current Schema ===")
	beforeSchema := analyzeSchema(gormDB)
	printSchemaSummary(beforeSchema)

	if *dryRun {
		fmt.Println("\n=== Dry Run Mode - Checking What Would Change ===")
		checkMigrationChanges(gormDB)
		return
	}

	// Run the auto-migration
	fmt.Println("\n=== Running Auto-Migration ===")
	err = runMigration(gormDB)
	if err != nil {
		log.Fatalf("Migration failed: %v", err)
	}
	fmt.Println("Migration completed successfully!")

	// Analyze schema after migration
	fmt.Println("\n=== Analyzing Schema After Migration ===")
	afterSchema := analyzeSchema(gormDB)
	printSchemaSummary(afterSchema)

	// Show what changed
	fmt.Println("\n=== Schema Changes ===")
	compareSchemas(beforeSchema, afterSchema)

	// Verify data integrity
	fmt.Println("\n=== Data Integrity Check ===")
	verifyDataIntegrity(gormDB)

	// Run functionality tests
	fmt.Println("\n=== Functionality Tests ===")
	runFunctionalityTests(gormDB)

	if *keepTestDB {
		fmt.Printf("\nTest completed. Test database saved at: %s\n", testDB)
	}
}

type TableSchema struct {
	Name    string
	Columns []ColumnInfo
	Indexes []string
}

type ColumnInfo struct {
	Name       string
	Type       string
	IsPrimary  bool
	IsNullable bool
}

func analyzeSchema(db *gorm.DB) map[string]TableSchema {
	schema := make(map[string]TableSchema)

	// Get all table names
	var tables []string
	db.Raw("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&tables)

	for _, table := range tables {
		tableSchema := TableSchema{Name: table}

		// Get columns
		rows, err := db.Raw(fmt.Sprintf("PRAGMA table_info(%s)", table)).Rows()
		if err != nil {
			continue
		}
		defer rows.Close()

		for rows.Next() {
			var cid int
			var name, dtype string
			var notnull, pk int
			var dflt interface{}

			err = rows.Scan(&cid, &name, &dtype, &notnull, &dflt, &pk)
			if err != nil {
				continue
			}

			tableSchema.Columns = append(tableSchema.Columns, ColumnInfo{
				Name:       name,
				Type:       dtype,
				IsPrimary:  pk == 1,
				IsNullable: notnull == 0,
			})
		}

		// Get indexes
		var indexes []struct {
			Name string
		}
		db.Raw(fmt.Sprintf("PRAGMA index_list(%s)", table)).Scan(&indexes)
		for _, idx := range indexes {
			tableSchema.Indexes = append(tableSchema.Indexes, idx.Name)
		}

		schema[table] = tableSchema
	}

	return schema
}

func printSchemaSummary(schema map[string]TableSchema) {
	for _, table := range schema {
		fmt.Printf("Table: %s (%d columns, %d indexes)\n", table.Name, len(table.Columns), len(table.Indexes))
	}
}

func compareSchemas(before, after map[string]TableSchema) {
	// Check for new tables
	for tableName := range after {
		if _, exists := before[tableName]; !exists {
			fmt.Printf("✅ New table created: %s\n", tableName)
		}
	}

	// Check for removed tables
	for tableName := range before {
		if _, exists := after[tableName]; !exists {
			fmt.Printf("❌ Table removed: %s\n", tableName)
		}
	}

	// Check for column changes in existing tables
	for tableName, afterTable := range after {
		beforeTable, exists := before[tableName]
		if !exists {
			continue
		}

		// Create maps for easier comparison
		beforeCols := make(map[string]ColumnInfo)
		for _, col := range beforeTable.Columns {
			beforeCols[col.Name] = col
		}

		afterCols := make(map[string]ColumnInfo)
		for _, col := range afterTable.Columns {
			afterCols[col.Name] = col
		}

		// Check for new columns
		for colName, col := range afterCols {
			if _, exists := beforeCols[colName]; !exists {
				fmt.Printf("✅ New column in %s: %s (%s)\n", tableName, colName, col.Type)
			}
		}

		// Check for removed columns
		for colName := range beforeCols {
			if _, exists := afterCols[colName]; !exists {
				fmt.Printf("❌ Column removed from %s: %s\n", tableName, colName)
			}
		}
	}
}

func runMigration(db *gorm.DB) error {
	launches := dbpkg.Launch{}
	userModel := users.User{}
	statsModel := stats.Statistics{}

	return db.AutoMigrate(&launches, &userModel, &statsModel)
}

func checkMigrationChanges(db *gorm.DB) {
	// This is a simplified version - GORM doesn't provide a built-in dry-run mode
	// In a real implementation, you might want to use db.Migrator() methods
	fmt.Println("Would migrate the following models:")
	fmt.Println("- Launch (db.Launch)")
	fmt.Println("- User (users.User)")
	fmt.Println("- Statistics (stats.Statistics)")
	fmt.Println("\nNote: Run without --dry-run to see actual changes")
}

func verifyDataIntegrity(db *gorm.DB) {
	// Check launch count
	var launchCount int64
	db.Model(&dbpkg.Launch{}).Count(&launchCount)
	fmt.Printf("✓ Launches: %d\n", launchCount)

	// Check user count
	var userCount int64
	db.Model(&users.User{}).Count(&userCount)
	fmt.Printf("✓ Users: %d\n", userCount)

	// Check statistics
	var statsCount int64
	db.Model(&stats.Statistics{}).Count(&statsCount)
	fmt.Printf("✓ Statistics records: %d\n", statsCount)

	// Check for orphaned records or referential integrity issues
	// Add more checks as needed based on your schema
}

func runFunctionalityTests(db *gorm.DB) {
	testsPassed := 0
	totalTests := 0

	// Test 1: Basic launch query
	totalTests++
	var testLaunch dbpkg.Launch
	err := db.First(&testLaunch).Error
	if err == nil {
		fmt.Printf("✓ Basic launch query\n")
		testsPassed++
	} else {
		fmt.Printf("✗ Basic launch query: %v\n", err)
	}

	// Test 2: User notification query
	totalTests++
	var notificationUsers []users.User
	err = db.Where("enabled24h = ? OR enabled12h = ? OR enabled1h = ? OR enabled5min = ?", 
		true, true, true, true).Limit(5).Find(&notificationUsers).Error
	if err == nil {
		fmt.Printf("✓ User notification query (found %d users)\n", len(notificationUsers))
		testsPassed++
	} else {
		fmt.Printf("✗ User notification query: %v\n", err)
	}

	// Test 3: Keyword fields (if they exist)
	totalTests++
	var keywordUsers []users.User
	err = db.Where("blocked_keywords IS NOT NULL OR allowed_keywords IS NOT NULL").Find(&keywordUsers).Error
	if err == nil {
		fmt.Printf("✓ Keyword fields query (found %d users with filters)\n", len(keywordUsers))
		testsPassed++
	} else {
		fmt.Printf("✗ Keyword fields query: %v\n", err)
	}

	// Test 4: Statistics query
	totalTests++
	var stat stats.Statistics
	err = db.First(&stat).Error
	if err == nil {
		fmt.Printf("✓ Statistics query\n")
		testsPassed++
	} else if err == gorm.ErrRecordNotFound {
		fmt.Printf("✓ Statistics query (no records)\n")
		testsPassed++
	} else {
		fmt.Printf("✗ Statistics query: %v\n", err)
	}

	fmt.Printf("\nTests passed: %d/%d\n", testsPassed, totalTests)
}