-- pihole-FTL.db as FTL database version 9 lays it out: every CREATE
-- statement is copied from the database dump FTL's own test suite starts from,
-- test/pihole-FTL.db.sql (identical in FTL v5.25.2 and master).
-- The rows are what FTL stores for the scenarios phonehome's tests check.
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE queries (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, type INTEGER NOT NULL, status INTEGER NOT NULL, domain TEXT NOT NULL, client TEXT NOT NULL, forward TEXT, additional_info TEXT);
CREATE TABLE ftl (id INTEGER PRIMARY KEY NOT NULL, value BLOB NOT NULL);
INSERT INTO ftl VALUES(0,9);
CREATE TABLE counters (id INTEGER PRIMARY KEY NOT NULL, value INTEGER NOT NULL);
CREATE TABLE message (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, type TEXT NOT NULL, message TEXT NOT NULL, blob1 BLOB, blob2 BLOB, blob3 BLOB, blob4 BLOB, blob5 BLOB);
CREATE TABLE IF NOT EXISTS "network" (id INTEGER PRIMARY KEY NOT NULL, hwaddr TEXT UNIQUE NOT NULL, interface TEXT NOT NULL, firstSeen INTEGER NOT NULL, lastQuery INTEGER NOT NULL, numQueries INTEGER NOT NULL, macVendor TEXT, aliasclient_id INTEGER);
CREATE TABLE IF NOT EXISTS "network_addresses" (network_id INTEGER NOT NULL, ip TEXT UNIQUE NOT NULL, lastSeen INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)), name TEXT, nameUpdated INTEGER, FOREIGN KEY(network_id) REFERENCES network(id));
CREATE INDEX idx_queries_timestamps ON queries (timestamp);
CREATE TABLE aliasclient (id INTEGER PRIMARY KEY NOT NULL, name TEXT NOT NULL, comment TEXT);
INSERT INTO "network" (id, hwaddr, interface, firstSeen, lastQuery, numQueries, macVendor) VALUES (1, 'aa:bb:cc:00:11:22', 'eth0', 1759398000, 1759402800, 7, 'Samsung Electronics Co.,Ltd');
INSERT INTO "network" (id, hwaddr, interface, firstSeen, lastQuery, numQueries) VALUES (2, 'ip-fd00::20', 'N/A', 1759398100, 1759402801, 2);
INSERT INTO "network_addresses" (network_id, ip, lastSeen, name) VALUES (1, '192.168.1.20', 1759402800, 'samsung-tv.lan');
INSERT INTO "network_addresses" (network_id, ip, lastSeen) VALUES (2, 'fd00::20', 1759402801);
-- Version 9 stores strings inline, whole-second timestamps, and the
-- CNAME that caused a block in additional_info.
INSERT INTO queries (id, timestamp, type, status, domain, client, forward, additional_info) VALUES
 (1, 1759402800, 1, 2, 'www.example.org', '192.168.1.20', '127.0.0.1#5335', NULL),
 (2, 1759402801, 2, 3, 'www.example.org', 'fd00::20', NULL, NULL),
 (3, 1759402802, 1, 1, 'ads.example.net', '192.168.1.20', NULL, NULL),
 (4, 1759402803, 1, 9, 'metrics.vendor.example', '192.168.1.21', '127.0.0.1#5335', 'tracker.example'),
 (5, 1759402804, 1, 16, 'use-application-dns.net', '192.168.1.21', NULL, NULL),
 (6, 1759402805, 1, 15, 'busy.example', '192.168.1.21', NULL, NULL),
 (7, 1759402806, 165, 2, 'example.net', '192.168.1.21', '127.0.0.1#5335', NULL),
 (8, 1759402807, 16, 17, 'stale.example', '192.168.1.21', NULL, NULL),
 (9, 1759402808, 1, 7, 'upstream-blocked.example', '192.168.1.21', '127.0.0.1#5335', NULL);
COMMIT;
