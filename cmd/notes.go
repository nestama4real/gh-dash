package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"charm.land/log/v2"
	"github.com/spf13/cobra"

	"github.com/dlvhdr/gh-dash/v4/internal/data"
)

var (
	notesAll  bool
	notesJSON bool
	notesAs   string
)

// notesCmd gives the review notes of the current worktree, written in the
// Work view of gh-dash, to coding agents.
var notesCmd = &cobra.Command{
	Use:   "notes",
	Short: "Print the review notes of the current worktree, for coding agents",
	Long: `Print the open review notes written in gh-dash's Work view on the branch
of the current worktree: file, lines, the diff excerpt, the note and its
replies. Closed (folded) discussions are left out, unless --all.

Reply to a note with: gh dash notes reply <id> "<text>" --as <agent>`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		log.SetLevel(log.ErrorLevel)
		meta, notes, err := currentNotes()
		if err != nil {
			return err
		}
		var shown []data.LocalNote
		for _, n := range notes {
			if notesAll || !n.Folded {
				shown = append(shown, n)
			}
		}
		if notesJSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if shown == nil {
				shown = []data.LocalNote{}
			}
			return enc.Encode(shown)
		}
		if len(shown) == 0 {
			fmt.Printf("No open review note on %s.\n", meta.Branch)
			return nil
		}
		fmt.Print(data.RenderNotesMarkdown(meta, shown))
		fmt.Printf("\n---\nReply to a note once handled, or to ask a question: "+
			"gh dash notes reply <id> \"<what you did>\" --as <your name>\n"+
			"Do not edit the notes files (%s) by hand.\n", data.NotesFile(meta.Repo, meta.Branch))
		return nil
	},
}

var notesReplyCmd = &cobra.Command{
	Use:   "reply <id> <text>",
	Short: "Reply to a review note of the current worktree",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		log.SetLevel(log.ErrorLevel)
		id, err := strconv.Atoi(strings.TrimPrefix(args[0], "#"))
		if err != nil {
			return fmt.Errorf("bad note id %q", args[0])
		}
		body := strings.TrimSpace(strings.Join(args[1:], " "))
		if body == "" {
			return fmt.Errorf("the reply is empty")
		}
		meta, err := data.CurrentWorktree(".")
		if err != nil {
			return err
		}
		if err := data.ReplyToLocalNote(meta, id, notesAs, body); err != nil {
			return err
		}
		fmt.Printf("Replied to note %d on %s.\n", id, meta.Branch)
		return nil
	},
}

func currentNotes() (data.NotesMeta, []data.LocalNote, error) {
	meta, err := data.CurrentWorktree(".")
	if err != nil {
		return meta, nil, err
	}
	return meta, data.LoadLocalNotes(meta.Repo, meta.Branch), nil
}

func init() {
	notesCmd.Flags().BoolVar(&notesAll, "all", false, "include the closed (folded) discussions")
	notesCmd.Flags().BoolVar(&notesJSON, "json", false, "print JSON")
	notesReplyCmd.Flags().StringVar(&notesAs, "as", "agent", "the name the reply is signed with")
	notesCmd.AddCommand(notesReplyCmd)
	rootCmd.AddCommand(notesCmd)
}
