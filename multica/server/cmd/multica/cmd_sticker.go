package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/stickers"
)

// sticker is the agent surface for discovering stickers to reference from
// structured message parts. The catalog is embedded in the binary, so search
// is local and instant — no API call. Agents don't "send" a sticker via this
// command; they use the printed id as a message part's sticker_id.
var stickerCmd = &cobra.Command{
	Use:   "sticker",
	Short: "Find stickers to use in chat, channels, and direct messages",
	Long: "Discover stickers from the platform sticker library. Search by mood " +
		"or keyword (Chinese or English), then use the printed id as a " +
		"structured message part's sticker_id.\n\n" +
		"Use stickers sparingly, to add a little emotion — not on every message.",
}

var stickerSearchCmd = &cobra.Command{
	Use:   "search <keyword>",
	Short: "Search stickers by mood or keyword (zh/en)",
	Long: "Search the sticker library by mood or keyword in Chinese or English " +
		"(e.g. `multica sticker search 开心`, `multica sticker search celebrate`).\n" +
		"Prints each match's id, name, and mood. Use the id in structured " +
		"message parts to send it.",
	Args: exactArgs(1),
	RunE: runStickerSearch,
}

var stickerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every sticker in the library",
	RunE:  runStickerList,
}

func init() {
	stickerSearchCmd.Flags().String("output", "table", "Output format: table or json")
	stickerListCmd.Flags().String("output", "table", "Output format: table or json")
	stickerCmd.AddCommand(stickerSearchCmd)
	stickerCmd.AddCommand(stickerListCmd)
}

func runStickerSearch(cmd *cobra.Command, args []string) error {
	results := stickers.Search(args[0])
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, stickersToJSON(results))
	}
	if len(results) == 0 {
		fmt.Fprintf(os.Stdout, "No stickers match %q.\n", args[0])
		fmt.Fprintf(os.Stdout, "Available moods: %s\n", strings.Join(stickers.Emotions(), ", "))
		return nil
	}
	printStickerTable(results)
	return nil
}

func runStickerList(cmd *cobra.Command, _ []string) error {
	all := stickers.All()
	if output, _ := cmd.Flags().GetString("output"); output == "json" {
		return cli.PrintJSON(os.Stdout, stickersToJSON(all))
	}
	printStickerTable(all)
	return nil
}

func printStickerTable(list []stickers.Sticker) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tMOOD")
	for _, s := range list {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", s.ID, stickerName(s), s.Emotion)
	}
	tw.Flush()
	fmt.Fprintf(os.Stdout, "\n%d sticker(s). Use an id as a structured message part sticker_id.\n", len(list))
}

func stickerName(s stickers.Sticker) string {
	if s.NameEn != "" && s.Name != s.NameEn {
		return fmt.Sprintf("%s / %s", s.Name, s.NameEn)
	}
	return s.Name
}

func stickersToJSON(list []stickers.Sticker) []map[string]any {
	out := make([]map[string]any, 0, len(list))
	for _, s := range list {
		out = append(out, map[string]any{
			"id":         s.ID,
			"sticker_id": s.ID,
			"token":      fmt.Sprintf(":sticker:%s:", s.ID), // Deprecated: kept for older clients.
			"name":       s.Name,
			"name_en":    s.NameEn,
			"emotion":    s.Emotion,
			"tags":       s.Tags,
		})
	}
	return out
}
