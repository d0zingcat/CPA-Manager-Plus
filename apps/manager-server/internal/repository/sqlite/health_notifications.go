package sqlite

import "database/sql"

func migrateHealthNotifications(db *sql.DB) error {
	_, err := db.Exec(`
 create table if not exists account_health_notification_state (
   credential_key text not null, dimension text not null, unhealthy integer not null,
   observed_at_ms integer not null, primary key(credential_key, dimension)
 );
 create table if not exists account_health_notification_outbox (
   id integer primary key autoincrement, stream_key text not null, payload text not null,
   created_at_ms integer not null, attempts integer not null default 0,
   next_attempt_ms integer not null, lease_until_ms integer not null default 0,
   status text not null default 'pending', last_error text not null default ''
 );
 create index if not exists account_health_notification_stream on account_health_notification_outbox(stream_key,status,id);
 create index if not exists account_health_notification_due on account_health_notification_outbox(status,next_attempt_ms,lease_until_ms);
 `)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	exists, err := tableHasColumn(tx, "account_health_notification_state", "rearm_at_ms")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(`alter table account_health_notification_state add column rearm_at_ms integer not null default 0`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
