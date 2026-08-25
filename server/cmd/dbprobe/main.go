package main

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"os"
)

func main() {
	c, err := pgx.Connect(context.Background(), "postgres://multica:multica@localhost:5432/multica?sslmode=disable")
	if err != nil { fmt.Println("conn err", err); os.Exit(1) }
	defer c.Close(context.Background())
	up, err := os.ReadFile("migrations/427_runtime_drop_owner_id.up.sql")
	if err != nil { fmt.Println("read err", err); os.Exit(1) }
	if _, err := c.Exec(context.Background(), string(up)); err != nil {
		fmt.Println("apply err:", err); os.Exit(1)
	}
	fmt.Println("427 up applied")
	for _, q := range []string{
		`SELECT to_regclass('computers'), to_regclass('computer_identity_owner')`,
		`SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name='agent_runtime' AND column_name='owner_id')`,
	} {
		rows, _ := c.Query(context.Background(), q)
		for rows.Next() { vals, _ := rows.Values(); fmt.Println(q, "=>", vals) }
		rows.Close()
	}
}
