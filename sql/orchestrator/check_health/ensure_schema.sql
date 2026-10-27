CREATE TABLE IF NOT EXISTS check_health_audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		account_number TEXT NOT NULL,
		timestamp DATETIME NOT NULL,
		total_positions INTEGER NOT NULL,
		protected_positions INTEGER NOT NULL,
		unprotected_positions INTEGER NOT NULL,
		orphan_orders INTEGER NOT NULL,
		status TEXT NOT NULL,
		notes TEXT,
		critical_findings INTEGER NOT NULL DEFAULT 0,
		warning_findings INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS check_health (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		category TEXT NOT NULL,
		severity TEXT NOT NULL DEFAULT 'WARNING',
		enabled INTEGER NOT NULL DEFAULT 1,
		query TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		created_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS check_health_finding (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		run_at DATETIME NOT NULL,
		account_number TEXT NOT NULL,
		check_name TEXT NOT NULL,
		category TEXT NOT NULL,
		severity TEXT NOT NULL,
		subject TEXT,
		detail TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_check_health_finding_run ON check_health_finding(run_at);
	CREATE INDEX IF NOT EXISTS idx_check_health_finding_acc_name ON check_health_finding(account_number, check_name);
