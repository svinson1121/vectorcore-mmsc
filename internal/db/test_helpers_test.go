package db

func testSQLiteConfig(dsn string) OpenOptions {
	return OpenOptions{
		Driver: "sqlite",
		DSN:    dsn,
	}
}
