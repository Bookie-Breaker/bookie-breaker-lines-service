package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	direction := flag.String("direction", "up", "Migration direction: up or down")
	steps := flag.Int("steps", 0, "Number of steps (0 = all)")
	flag.Parse()

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "pgx5://bookiebreaker:localdev@localhost:5432/bookiebreaker?search_path=lines"
	}

	m, err := migrate.New("file://migrations", databaseURL)
	if err != nil {
		log.Fatalf("failed to create migrate instance: %v", err)
	}

	switch *direction {
	case "up":
		if *steps > 0 {
			err = m.Steps(*steps)
		} else {
			err = m.Up()
		}
	case "down":
		if *steps > 0 {
			err = m.Steps(-*steps)
		} else {
			err = m.Down()
		}
	default:
		log.Fatalf("unknown direction: %s", *direction)
	}

	if err != nil && err != migrate.ErrNoChange {
		log.Fatalf("migration %s failed: %v", *direction, err)
	}

	version, dirty, _ := m.Version()
	fmt.Printf("migration complete: version=%d dirty=%v\n", version, dirty)
}
