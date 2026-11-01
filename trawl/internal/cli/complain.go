package cli

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agents "github.com/opentrawl/opentrawl/trawlers/agents"
	"github.com/opentrawl/opentrawl/trawlkit"
	complaint "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/complaint"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Complaints stay in the state root, one text protobuf file each, so the owner
// and their agents can read them with trawl complaints or any editor.
const trawlComplaintsFolderName = "complaints"

type ComplainCmd struct {
	Words []string `arg:"" name:"words" help:"What was hard to use or read, and what you expected instead"`
	About string   `name:"about" placeholder:"COMMAND" help:"The trawl command the complaint is about"`
}

type ComplaintsCmd struct {
	Limit int `name:"limit" default:"20" placeholder:"COUNT" help:"Maximum number of complaints"`
}

func (command *ComplainCmd) Run(r *Runtime) error {
	complaintText := strings.TrimSpace(strings.Join(command.Words, " "))
	if complaintText == "" {
		return render.CommandNeedsArguments("complain", []string{"WORDS"})
	}
	now := r.now()
	savedComplaint := &complaint.TrawlComplaint{
		ComplaintTime:                         timestamppb.New(now),
		ComplaintText:                         complaintText,
		TrawlCommandComplainedAbout:           strings.TrimSpace(command.About),
		ComplainingAgentConversationReference: agents.ReferenceOfAgentConversationRunningThisProcess(),
	}
	encoded, err := prototext.MarshalOptions{Multiline: true}.Marshal(savedComplaint)
	if err != nil {
		return err
	}
	folder := filepath.Join(r.stateRoot, trawlComplaintsFolderName)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	fileName := now.UTC().Format("2006-01-02T15-04-05.000000000Z") + ".txtpb"
	// Written under a dot name and renamed, so a reader never sees half a file.
	partialPath := filepath.Join(folder, "."+fileName)
	if err := os.WriteFile(partialPath, encoded, 0o600); err != nil {
		return err
	}
	if err := os.Rename(partialPath, filepath.Join(folder, fileName)); err != nil {
		return err
	}
	return r.output.WriteTrawlComplaintSaved()
}

func (command *ComplaintsCmd) Run(r *Runtime) error {
	if command.Limit < 1 {
		return render.LimitMustBeAtLeastOne()
	}
	folder := filepath.Join(r.stateRoot, trawlComplaintsFolderName)
	entries, err := os.ReadDir(folder)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	response := &complaint.TrawlComplaintListResponse{}
	var complainingConversationReferences []*trawlkit.CanonicalArchiveRecordReference
	var fileNames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".txtpb") && !strings.HasPrefix(entry.Name(), ".") {
			fileNames = append(fileNames, entry.Name())
		}
	}
	// File names are complaint times, so name order is time order.
	sort.Sort(sort.Reverse(sort.StringSlice(fileNames)))
	response.TotalTrawlComplaintCount = uint64(len(fileNames))
	for _, fileName := range fileNames[:min(len(fileNames), command.Limit)] {
		encoded, err := os.ReadFile(filepath.Join(folder, fileName))
		if err != nil {
			return err
		}
		savedComplaint := &complaint.TrawlComplaint{}
		if err := prototext.Unmarshal(encoded, savedComplaint); err != nil {
			return err
		}
		response.ListedTrawlComplaintsNewestFirst = append(response.ListedTrawlComplaintsNewestFirst, &complaint.ListedTrawlComplaint{TrawlComplaint: savedComplaint})
		if reference := savedComplaint.GetComplainingAgentConversationReference(); reference != nil {
			complainingConversationReferences = append(complainingConversationReferences, reference)
		}
	}
	links, err := agents.TrawlLinksForArchivedAgentConversations(r.ctx, r.stateRoot, complainingConversationReferences)
	if err != nil {
		return err
	}
	for _, listed := range response.ListedTrawlComplaintsNewestFirst {
		reference := trawlkit.CanonicalArchiveRecordReferenceText(listed.GetTrawlComplaint().GetComplainingAgentConversationReference())
		listed.ComplainingAgentConversationTrawlLink = links[reference]
	}
	return r.output.WriteTrawlComplaintList(response, command.Limit)
}
