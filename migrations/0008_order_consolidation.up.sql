CREATE TABLE order_consolidations (
    order_ref TEXT PRIMARY KEY,
    required_lines TEXT[] NOT NULL DEFAULT '{}',
    arrived_lines  TEXT[] NOT NULL DEFAULT '{}'
);
