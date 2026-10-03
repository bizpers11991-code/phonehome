-- pihole-FTL.db as FTL database version 12 lays it out: every CREATE
-- statement is copied from the schema FTL v5.25.2's test suite expects after
-- migrating (test/test_suite.bats, "pihole-FTL.db schema is as expected").
-- The rows are what FTL stores for the scenarios phonehome's tests check.
PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE IF NOT EXISTS "query_storage" (id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp INTEGER NOT NULL, type INTEGER NOT NULL, status INTEGER NOT NULL, domain INTEGER NOT NULL, client INTEGER NOT NULL, forward INTEGER, additional_info INTEGER, reply_type INTEGER, reply_time REAL, dnssec INTEGER);
CREATE INDEX idx_queries_timestamps ON "query_storage" (timestamp);
CREATE TABLE ftl (id INTEGER PRIMARY KEY NOT NULL, value BLOB NOT NULL);
INSERT INTO ftl VALUES(0,12);
CREATE TABLE counters (id INTEGER PRIMARY KEY NOT NULL, value INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS "network" (id INTEGER PRIMARY KEY NOT NULL, hwaddr TEXT UNIQUE NOT NULL, interface TEXT NOT NULL, firstSeen INTEGER NOT NULL, lastQuery INTEGER NOT NULL, numQueries INTEGER NOT NULL, macVendor TEXT, aliasclient_id INTEGER);
CREATE TABLE IF NOT EXISTS "network_addresses" (network_id INTEGER NOT NULL, ip TEXT UNIQUE NOT NULL, lastSeen INTEGER NOT NULL DEFAULT (cast(strftime('%s', 'now') as int)), name TEXT, nameUpdated INTEGER, FOREIGN KEY(network_id) REFERENCES network(id));
CREATE TABLE aliasclient (id INTEGER PRIMARY KEY NOT NULL, name TEXT NOT NULL, comment TEXT);
CREATE VIEW queries AS SELECT id, timestamp, type, status, CASE typeof(domain) WHEN 'integer' THEN (SELECT domain FROM domain_by_id d WHERE d.id = q.domain) ELSE domain END domain,CASE typeof(client) WHEN 'integer' THEN (SELECT ip FROM client_by_id c WHERE c.id = q.client) ELSE client END client,CASE typeof(forward) WHEN 'integer' THEN (SELECT forward FROM forward_by_id f WHERE f.id = q.forward) ELSE forward END forward,CASE typeof(additional_info) WHEN 'integer' THEN (SELECT content FROM addinfo_by_id a WHERE a.id = q.additional_info) ELSE additional_info END additional_info, reply_type, reply_time, dnssec FROM query_storage q;
CREATE TABLE domain_by_id (id INTEGER PRIMARY KEY, domain TEXT NOT NULL);
CREATE TABLE client_by_id (id INTEGER PRIMARY KEY, ip TEXT NOT NULL, name TEXT);
CREATE TABLE forward_by_id (id INTEGER PRIMARY KEY, forward TEXT NOT NULL);
CREATE UNIQUE INDEX domain_by_id_domain_idx ON domain_by_id(domain);
CREATE UNIQUE INDEX client_by_id_client_idx ON client_by_id(ip,name);
CREATE TABLE addinfo_by_id (id INTEGER PRIMARY KEY, type INTEGER NOT NULL, content NOT NULL);
CREATE UNIQUE INDEX addinfo_by_id_idx ON addinfo_by_id(type,content);
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
-- Rows 1-3 were migrated from the version 9 queries table (strings
-- inline); the rest were written since (ids into the *_by_id tables).
INSERT INTO query_storage (id, timestamp, type, status, domain, client, forward, additional_info, reply_type, reply_time, dnssec) VALUES
 (1, 1759402800, 1, 2, 'www.example.org', '192.168.1.20', '127.0.0.1#5335', NULL, NULL, NULL, NULL),
 (2, 1759402801, 2, 3, 'www.example.org', 'fd00::20', NULL, NULL, NULL, NULL, NULL),
 (3, 1759402802, 1, 1, 'ads.example.net', '192.168.1.20', NULL, NULL, NULL, NULL, NULL),
 (4, 1759402803, 1, 9, 3, 3, 1, 1, 3, 0.012, 0),
 (5, 1759402804, 1, 16, 4, 3, NULL, NULL, 2, 0.0001, 0),
 (6, 1759402805, 1, 15, 5, 3, NULL, NULL, 0, NULL, 0),
 (7, 1759402806, 165, 2, 6, 3, 1, NULL, 1, 0.03, 0),
 (8, 1759402807, 16, 17, 7, 3, NULL, NULL, 4, 0.0002, 0),
 (9, 1759402808, 1, 7, 8, 3, 1, NULL, 9, 0.02, 0);
COMMIT;
