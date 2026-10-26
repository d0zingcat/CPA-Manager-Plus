package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestHealthNotificationMigrationPreservesExistingState(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`create table account_health_notification_state(credential_key text not null,dimension text not null,unhealthy integer not null,observed_at_ms integer not null,primary key(credential_key,dimension)); insert into account_health_notification_state values('one','quota:unknown:all',1,123)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := migrateHealthNotifications(db); err != nil {
			t.Fatal(err)
		}
	}
	var observed, rearm int64
	if err := db.QueryRow(`select observed_at_ms,rearm_at_ms from account_health_notification_state where credential_key='one'`).Scan(&observed, &rearm); err != nil {
		t.Fatal(err)
	}
	if observed != 123 || rearm != 0 {
		t.Fatal("migration changed previous state")
	}
}
