-- pihole-FTL.db as FTL database version 22 lays it out: every CREATE
-- statement is copied from the schema current FTL's test suite expects (master
-- 0bf029baf174, test/test_suite.bats, "pihole-FTL.db schema is as expected").
-- The rows are what FTL stores for the scenarios phonehome's tests check.
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE IF NOT EXISTS "query_storage" (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, type INTEGER NOT NULL, status INTEGER NOT NULL, domain INTEGER NOT NULL, client INTEGER NOT NULL, forward INTEGER, additional_info INTEGER, reply_type INTEGER, reply_time REAL, dnssec INTEGER, list_id INTEGER, ede INTEGER);
CREATE INDEX idx_queries_timestamps ON "query_storage" (timestamp);
CREATE TABLE ftl (id INTEGER PRIMARY KEY NOT NULL, value BLOB NOT NULL, description TEXT);
INSERT INTO ftl VALUES(0,22,'Database version');
CREATE TABLE counters (id INTEGER PRIMARY KEY NOT NULL, value INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS "network" (id INTEGER PRIMARY KEY NOT NULL, hwaddr TEXT UNIQUE NOT NULL, interface TEXT NOT NULL, firstSeen INTEGER NOT NULL, lastQuery INTEGER NOT NULL, numQueries INTEGER NOT NULL, macVendor TEXT, aliasclient_id INTEGER);
CREATE TABLE IF NOT EXISTS "network_addresses" (network_id INTEGER NOT NULL, ip TEXT UNIQUE NOT NULL, lastSeen INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)), name TEXT, nameUpdated INTEGER, FOREIGN KEY(network_id) REFERENCES network(id));
CREATE TABLE aliasclient (id INTEGER PRIMARY KEY NOT NULL, name TEXT NOT NULL, comment TEXT);
CREATE VIEW queries AS SELECT q.id, q.timestamp, q.type, q.status, COALESCE(d.domain, q.domain) AS domain, COALESCE(c.ip, q.client) AS client, COALESCE(f.forward, q.forward) AS forward, COALESCE(a.content, q.additional_info) AS additional_info, q.reply_type, q.reply_time, q.dnssec, q.list_id, q.ede FROM query_storage q LEFT JOIN domain_by_id d ON q.domain = d.id LEFT JOIN client_by_id c ON q.client = c.id LEFT JOIN forward_by_id f ON q.forward = f.id LEFT JOIN addinfo_by_id a ON q.additional_info = a.id;
CREATE TABLE domain_by_id (id INTEGER PRIMARY KEY, domain TEXT NOT NULL);
CREATE TABLE client_by_id (id INTEGER PRIMARY KEY, ip TEXT NOT NULL, name TEXT);
CREATE TABLE forward_by_id (id INTEGER PRIMARY KEY, forward TEXT NOT NULL);
CREATE UNIQUE INDEX domain_by_id_domain_idx ON domain_by_id(domain);
CREATE UNIQUE INDEX client_by_id_client_idx ON client_by_id(ip,name);
CREATE TABLE addinfo_by_id (id INTEGER PRIMARY KEY, type INTEGER NOT NULL, content NOT NULL);
CREATE UNIQUE INDEX addinfo_by_id_idx ON addinfo_by_id(type,content);
CREATE TABLE session (id INTEGER PRIMARY KEY, login_at TIMESTAMP NOT NULL, valid_until TIMESTAMP NOT NULL, remote_addr TEXT NOT NULL, user_agent TEXT, sid TEXT NOT NULL, csrf TEXT NOT NULL, tls_login BOOL, tls_mixed BOOL, app BOOL, cli BOOL, x_forwarded_for TEXT);
CREATE INDEX network_addresses_network_id_index ON network_addresses (network_id);
INSERT INTO "network" (id, hwaddr, interface, firstSeen, lastQuery, numQueries, macVendor) VALUES (1, 'aa:bb:cc:00:11:22', 'eth0', 1759398000, 1759402800, 7, 'Samsung Electronics Co.,Ltd');
INSERT INTO "network" (id, hwaddr, interface, firstSeen, lastQuery, numQueries) VALUES (2, 'ip-fd00::20', 'N/A', 1759398100, 1759402801, 2);
INSERT INTO "network_addresses" (network_id, ip, lastSeen, name) VALUES (1, '192.168.1.20', 1759402800, 'samsung-tv.lan');
INSERT INTO "network_addresses" (network_id, ip, lastSeen) VALUES (2, 'fd00::20', 1759402801);
INSERT INTO domain_by_id VALUES(1,'www.example.org');
INSERT INTO domain_by_id VALUES(2,'ads.example.net');
INSERT INTO domain_by_id VALUES(3,'metrics.vendor.example');
INSERT INTO domain_by_id VALUES(4,'use-application-dns.net');
INSERT INTO domain_by_id VALUES(5,'busy.example');
INSERT INTO domain_by_id VALUES(6,'example.net');
INSERT INTO domain_by_id VALUES(7,'stale.example');
INSERT INTO domain_by_id VALUES(8,'upstream-blocked.example');
INSERT INTO client_by_id VALUES(1,'192.168.1.20','samsung-tv.lan');
INSERT INTO client_by_id VALUES(2,'fd00::20','');
INSERT INTO client_by_id VALUES(3,'192.168.1.21','');
INSERT INTO forward_by_id VALUES(1,'127.0.0.1#5335');
INSERT INTO addinfo_by_id VALUES(1,1,'tracker.example');
-- Rows 1-3 date from Pi-hole v5 (whole seconds, strings inline); the
-- rest were written by v6: fractional timestamps, ids into the *_by_id
-- tables, list_id, and status 18 (EXTERNAL_BLOCKED_EDE15), new in v6.
INSERT INTO query_storage (id, timestamp, type, status, domain, client, forward, additional_info, reply_type, reply_time, dnssec, list_id, ede) VALUES
 (1, 1759402800, 1, 2, 'www.example.org', '192.168.1.20', '127.0.0.1#5335', NULL, NULL, NULL, NULL, NULL, NULL),
 (2, 1759402801, 2, 3, 'www.example.org', 'fd00::20', NULL, NULL, NULL, NULL, NULL, NULL, NULL),
 (3, 1759402802, 1, 1, 'ads.example.net', '192.168.1.20', NULL, NULL, NULL, NULL, NULL, NULL, NULL),
 (4, 1759402803.25, 1, 9, 3, 3, 1, 1, 3, 0.012, 0, 12, -1),
 (5, 1759402804.5, 1, 16, 4, 3, NULL, NULL, 2, 0.0001, 0, NULL, -1),
 (6, 1759402805.75, 1, 15, 5, 3, NULL, NULL, 0, NULL, 0, NULL, -1),
 (7, 1759402806.125, 165, 2, 6, 3, 1, NULL, 1, 0.03, 0, NULL, -1),
 (8, 1759402807.0625, 16, 17, 7, 3, NULL, NULL, 4, 0.0002, 0, NULL, 3),
 (9, 1759402808.5, 1, 7, 8, 3, 1, NULL, 9, 0.02, 0, NULL, -1),
 (10, 1759402809.5, 0, 18, 8, 2, 1, NULL, 2, 0.02, 0, NULL, 15);
COMMIT;
