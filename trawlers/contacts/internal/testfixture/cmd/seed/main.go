package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

func main() {
	archivePath := flag.String("archive", "", "synthetic Contacts archive path")
	externalID := flag.String("id", "", "synthetic source identifier")
	name := flag.String("name", "", "synthetic contact name")
	email := flag.String("email", "", "synthetic contact email")
	flag.Parse()
	if flag.NArg() != 0 || strings.TrimSpace(*archivePath) == "" || strings.TrimSpace(*externalID) == "" || strings.TrimSpace(*name) == "" || strings.TrimSpace(*email) == "" {
		fmt.Fprintln(os.Stderr, "usage: seed --archive PATH --id ID --name NAME --email EMAIL")
		os.Exit(2)
	}

	store, err := archive.Open(context.Background(), *archivePath)
	if err == nil {
		_, err = store.SyncContactSnapshot(context.Background(), "synthetic", []model.SourceContact{{
			ExternalID: *externalID,
			Name:       *name,
			Emails:     []model.ContactValue{{Value: *email}},
		}}, time.Date(2026, 7, 9, 10, 0, 0, 0, time.UTC))
	}
	var people []model.Person
	if err == nil {
		people, err = store.People(context.Background())
	}
	if store != nil {
		if closeErr := store.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	indexStore, err := ckstore.Open(context.Background(), ckstore.Options{Path: *archivePath})
	if err == nil {
		records := make([]trawlkit.ShortRefRecord, 0, len(people))
		for _, person := range people {
			records = append(records, trawlkit.ShortRefRecord{Ref: archive.PersonRef(person.ID)})
		}
		_, err = (&trawlkit.Request{Store: indexStore}).AssignShortRefs(context.Background(), records)
	}
	if indexStore != nil {
		if closeErr := indexStore.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
