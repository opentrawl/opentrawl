package qa

import "time"

func createPhotosFixture(libraryPath string) error {
	dbPath := libraryPath + "/database/Photos.sqlite"
	db, err := openSQLite(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if err := execAll(db,
		`create table ZASSET (
			Z_PK integer primary key,
			ZUUID varchar,
			ZKIND integer,
			ZKINDSUBTYPE integer,
			ZDATECREATED timestamp,
			ZMODIFICATIONDATE timestamp,
			ZADDEDDATE timestamp,
			ZWIDTH integer,
			ZHEIGHT integer,
			ZDURATION float,
			ZFAVORITE integer,
			ZHIDDEN integer,
			ZAVALANCHEUUID varchar,
			ZLATITUDE float,
			ZLONGITUDE float,
			ZUNIFORMTYPEIDENTIFIER varchar,
			ZFILENAME varchar,
			ZTRASHEDSTATE integer
		)`,
		`create table ZADDITIONALASSETATTRIBUTES (
			ZASSET integer,
			ZTIMEZONENAME varchar,
			ZGPSHORIZONTALACCURACY float,
			ZORIGINALFILENAME varchar
		)`,
		`create table ZEXTENDEDATTRIBUTES (
			ZASSET integer,
			ZCAMERAMAKE varchar,
			ZCAMERAMODEL varchar,
			ZLENSMODEL varchar,
			ZFOCALLENGTH float,
			ZFOCALLENGTHIN35MM float,
			ZAPERTURE float,
			ZSHUTTERSPEED float,
			ZISO float
		)`,
		`create table ZINTERNALRESOURCE (
			ZASSET integer,
			ZRESOURCETYPE integer,
			ZCOMPACTUTI varchar,
			ZDATALENGTH integer,
			ZSTABLEHASH varchar,
			ZFINGERPRINT varchar,
			ZLOCALAVAILABILITY integer,
			ZREMOTEAVAILABILITY integer,
			ZVERSION integer
		)`,
		`create table ZGENERICALBUM (
			Z_PK integer primary key,
			ZUUID varchar,
			ZTITLE varchar,
			ZKIND integer,
			ZCLOUDALBUMSUBTYPE integer,
			ZTRASHEDSTATE integer
		)`,
		`create table Z_34ASSETS (
			Z_34ALBUMS integer,
			Z_3ASSETS integer
		)`,
	); err != nil {
		return err
	}
	created := coreDate(time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC))
	if _, err := db.Exec(`
insert into ZASSET(Z_PK, ZUUID, ZKIND, ZKINDSUBTYPE, ZDATECREATED, ZMODIFICATIONDATE, ZADDEDDATE, ZWIDTH, ZHEIGHT, ZDURATION, ZFAVORITE, ZHIDDEN, ZAVALANCHEUUID, ZLATITUDE, ZLONGITUDE, ZUNIFORMTYPEIDENTIFIER, ZFILENAME, ZTRASHEDSTATE)
values (1, 'fixture-uuid-1', 0, 0, ?, ?, ?, 4032, 3024, 0, 1, 0, '', 52.3676, 4.9041, 'public.heic', 'synthetic.heic', 0)
`, created, created, created); err != nil {
		return err
	}
	return execAll(db,
		`insert into ZADDITIONALASSETATTRIBUTES(ZASSET, ZTIMEZONENAME, ZGPSHORIZONTALACCURACY, ZORIGINALFILENAME) values (1, 'Europe/Amsterdam', 8.25, 'synthetic.heic')`,
		`insert into ZEXTENDEDATTRIBUTES(ZASSET, ZCAMERAMAKE, ZCAMERAMODEL, ZLENSMODEL, ZFOCALLENGTH, ZFOCALLENGTHIN35MM, ZAPERTURE, ZSHUTTERSPEED, ZISO) values (1, 'Apple', 'iPhone 15 Pro', 'back camera', 6.86, 24, 1.8, 0.008333333333333333, 64)`,
		`insert into ZINTERNALRESOURCE(ZASSET, ZRESOURCETYPE, ZCOMPACTUTI, ZDATALENGTH, ZSTABLEHASH, ZFINGERPRINT, ZLOCALAVAILABILITY, ZREMOTEAVAILABILITY, ZVERSION) values (1, 0, 'public.heic', 12345, 'stable-hash', '', 0, 1, 1)`,
		`insert into ZGENERICALBUM(Z_PK, ZUUID, ZTITLE, ZKIND, ZCLOUDALBUMSUBTYPE, ZTRASHEDSTATE) values (10, 'album-uuid-1', 'Launch Album', 2, 0, 0)`,
		`insert into Z_34ASSETS(Z_34ALBUMS, Z_3ASSETS) values (10, 1)`,
	)
}
