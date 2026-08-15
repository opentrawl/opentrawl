package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type sourceArchiveSnapshotSHA256 string

type searchResearchSourceSnapshot struct {
	stateRoot       string
	contentSHA256   sourceArchiveSnapshotSHA256
	selectedTrawler registeredTrawlerName
}

func calculateSearchResearchSourceSnapshot(
	stateRoot string,
	selectedTrawler registeredTrawlerName,
) (searchResearchSourceSnapshot, error) {
	absoluteStateRoot := filepath.Clean(strings.TrimSpace(stateRoot))
	if strings.TrimSpace(stateRoot) == "" || !filepath.IsAbs(absoluteStateRoot) {
		return searchResearchSourceSnapshot{}, errors.New("source snapshot state root must be absolute")
	}
	if selectedTrawler != "" && !isSearchResearchTrawler(selectedTrawler) {
		return searchResearchSourceSnapshot{}, fmt.Errorf("unknown research trawler %q", selectedTrawler)
	}
	digest := sha256.New()
	for _, source := range searchableRecordExportTrawlers() {
		if selectedTrawler != "" && source.registeredTrawler != selectedTrawler {
			continue
		}
		relativeDatabasePath := filepath.Join(
			string(source.registeredTrawler),
			string(source.registeredTrawler)+".db",
		)
		databaseFile, err := os.Open(filepath.Join(absoluteStateRoot, relativeDatabasePath))
		if err != nil {
			return searchResearchSourceSnapshot{}, fmt.Errorf("open %s source snapshot database: %w", source.registeredTrawler, err)
		}
		databaseDigest := sha256.New()
		_, copyError := io.Copy(databaseDigest, databaseFile)
		closeError := databaseFile.Close()
		if err := errors.Join(copyError, closeError); err != nil {
			return searchResearchSourceSnapshot{}, fmt.Errorf("hash %s source snapshot database: %w", source.registeredTrawler, err)
		}
		if _, err := io.WriteString(digest, filepath.ToSlash(relativeDatabasePath)+"\x00"); err != nil {
			return searchResearchSourceSnapshot{}, err
		}
		if _, err := digest.Write(databaseDigest.Sum(nil)); err != nil {
			return searchResearchSourceSnapshot{}, err
		}
	}
	return searchResearchSourceSnapshot{
		stateRoot:       absoluteStateRoot,
		contentSHA256:   sourceArchiveSnapshotSHA256(hex.EncodeToString(digest.Sum(nil))),
		selectedTrawler: selectedTrawler,
	}, nil
}

func establishSearchResearchSourceSnapshot(
	database *sql.DB,
	snapshot searchResearchSourceSnapshot,
) error {
	var existingStateRoot, existingContentSHA256 string
	err := database.QueryRow(`
		select source_archive_snapshot_state_root, source_archive_snapshot_sha256
		from corpus_metadata
		where singleton = 1`).Scan(&existingStateRoot, &existingContentSHA256)
	if err != nil {
		return err
	}
	if existingStateRoot == "" && existingContentSHA256 == "" {
		_, err = database.Exec(`
			update corpus_metadata
			set source_archive_snapshot_state_root = ?, source_archive_snapshot_sha256 = ?
			where singleton = 1`, snapshot.stateRoot, snapshot.contentSHA256)
		return err
	}
	if existingStateRoot != snapshot.stateRoot || sourceArchiveSnapshotSHA256(existingContentSHA256) != snapshot.contentSHA256 {
		return errors.New("existing corpus belongs to a different source archive snapshot")
	}
	return nil
}

func readSearchResearchSourceSnapshot(database *sql.DB) (searchResearchSourceSnapshot, error) {
	var snapshot searchResearchSourceSnapshot
	err := database.QueryRow(`
		select metadata.source_archive_snapshot_state_root,
		       metadata.source_archive_snapshot_sha256,
		       scope.selected_registered_trawler
		from corpus_metadata metadata
		join corpus_projection_scope scope on scope.singleton = metadata.singleton
		where metadata.singleton = 1`).Scan(
		&snapshot.stateRoot,
		&snapshot.contentSHA256,
		&snapshot.selectedTrawler,
	)
	if err != nil {
		return searchResearchSourceSnapshot{}, err
	}
	if snapshot.stateRoot == "" || snapshot.contentSHA256 == "" {
		return searchResearchSourceSnapshot{}, errors.New("corpus source archive snapshot is missing")
	}
	return snapshot, nil
}

func verifySearchResearchSourceSnapshot(database *sql.DB) (searchResearchSourceSnapshot, error) {
	recordedSnapshot, err := readSearchResearchSourceSnapshot(database)
	if err != nil {
		return searchResearchSourceSnapshot{}, err
	}
	currentSnapshot, err := calculateSearchResearchSourceSnapshot(
		recordedSnapshot.stateRoot,
		recordedSnapshot.selectedTrawler,
	)
	if err != nil {
		return searchResearchSourceSnapshot{}, err
	}
	if currentSnapshot.contentSHA256 != recordedSnapshot.contentSHA256 {
		return searchResearchSourceSnapshot{}, errors.New("source archive snapshot changed; rebuild the search corpus and index")
	}
	return recordedSnapshot, nil
}
