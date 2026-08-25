ALTER TABLE packages ADD COLUMN scanned_hazard_classes INTEGER[] NOT NULL DEFAULT '{}';
