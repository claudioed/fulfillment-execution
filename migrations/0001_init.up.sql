CREATE TABLE stations (
    id           TEXT PRIMARY KEY,
    capabilities TEXT[] NOT NULL DEFAULT '{}',
    occupant     TEXT
);

CREATE TABLE tasks (
    id                    TEXT PRIMARY KEY,
    task_type             TEXT NOT NULL,
    status                TEXT NOT NULL,
    cpt                   TIMESTAMPTZ NOT NULL,
    order_ref             TEXT NOT NULL,
    required_capabilities TEXT[] NOT NULL DEFAULT '{}',
    lease_station_id      TEXT,
    lease_expiry          TIMESTAMPTZ
);

CREATE INDEX idx_tasks_type_status_cpt ON tasks (task_type, status, cpt);

CREATE TABLE packages (
    id               TEXT PRIMARY KEY,
    order_ref        TEXT NOT NULL,
    status           TEXT NOT NULL,
    scanned_contents TEXT[] NOT NULL DEFAULT '{}'
);

CREATE TABLE domain_events (
    id          BIGSERIAL PRIMARY KEY,
    event_name  TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    payload     JSONB NOT NULL
);
