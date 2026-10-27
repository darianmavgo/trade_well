SELECT account_number, severity, check_name, IFNULL(subject,''), detail FROM check_health_finding WHERE id > ? ORDER BY id
