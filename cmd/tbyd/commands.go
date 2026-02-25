package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kalambet/tbyd/internal/config"
)

// --- ingest ---

var ingestCmd = &cobra.Command{
	Use:   "ingest",
	Short: "Ingest content into the knowledge base",
	Long: `Ingest content into the knowledge base.

Examples:
  tbyd ingest --text "I prefer Go for backend services" --tags preference
  tbyd ingest --url https://example.com/article --tags research
  tbyd ingest --file ./notes.md --title "My notes" --tags notes`,
	RunE: func(cmd *cobra.Command, args []string) error {
		text, _ := cmd.Flags().GetString("text")
		url, _ := cmd.Flags().GetString("url")
		file, _ := cmd.Flags().GetString("file")
		title, _ := cmd.Flags().GetString("title")
		tagsStr, _ := cmd.Flags().GetString("tags")

		if text == "" && url == "" && file == "" {
			return fmt.Errorf("one of --text, --url, or --file is required")
		}

		var tags []string
		if tagsStr != "" {
			tags = strings.Split(tagsStr, ",")
			for i := range tags {
				tags[i] = strings.TrimSpace(tags[i])
			}
		}

		req := map[string]any{
			"source": "cli",
		}
		if tags != nil {
			req["tags"] = tags
		}
		if title != "" {
			req["title"] = title
		}

		switch {
		case text != "":
			req["type"] = "text"
			req["content"] = text
		case url != "":
			req["type"] = "url"
			req["url"] = url
		case file != "":
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("reading file: %w", err)
			}
			req["type"] = "text"
			req["content"] = string(data)
			if title == "" {
				req["title"] = file
			}
		}

		client, err := newAPIClient()
		if err != nil {
			return err
		}

		resp, err := client.post("/ingest", req)
		if err != nil {
			return err
		}

		var result map[string]string
		if err := decodeJSON(resp, &result); err != nil {
			return err
		}

		printSuccess("Queued doc %s", result["id"])
		return nil
	},
}

func init() {
	ingestCmd.Flags().String("text", "", "text content to ingest")
	ingestCmd.Flags().String("url", "", "URL to fetch and ingest")
	ingestCmd.Flags().String("file", "", "file path to ingest")
	ingestCmd.Flags().String("title", "", "title for the document")
	ingestCmd.Flags().String("tags", "", "comma-separated tags")
}

// --- profile ---

var profileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Manage user profile",
}

var profileShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current profile as JSON",
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		resp, err := client.get("/profile")
		if err != nil {
			return err
		}

		var profile any
		if err := decodeJSON(resp, &profile); err != nil {
			return err
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(profile)
	},
}

var profileSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a profile field",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]

		client, err := newAPIClient()
		if err != nil {
			return err
		}

		body := map[string]any{key: value}
		resp, err := client.patch("/profile", body)
		if err != nil {
			return err
		}

		var result map[string]string
		if err := decodeJSON(resp, &result); err != nil {
			return err
		}

		printSuccess("Set %s = %s", key, value)
		return nil
	},
}

var profileEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open profile JSON in $EDITOR",
	RunE: func(cmd *cobra.Command, args []string) error {
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}

		client, err := newAPIClient()
		if err != nil {
			return err
		}

		resp, err := client.get("/profile")
		if err != nil {
			return err
		}

		var profile any
		if err := decodeJSON(resp, &profile); err != nil {
			return err
		}

		data, err := json.MarshalIndent(profile, "", "  ")
		if err != nil {
			return err
		}

		tmpFile, err := os.CreateTemp("", "tbyd-profile-*.json")
		if err != nil {
			return fmt.Errorf("creating temp file: %w", err)
		}
		tmpPath := tmpFile.Name()
		defer os.Remove(tmpPath)

		if _, err := tmpFile.Write(data); err != nil {
			tmpFile.Close()
			return err
		}
		tmpFile.Close()

		editorCmd := exec.Command(editor, tmpPath)
		editorCmd.Stdin = os.Stdin
		editorCmd.Stdout = os.Stdout
		editorCmd.Stderr = os.Stderr
		if err := editorCmd.Run(); err != nil {
			return fmt.Errorf("editor exited with error: %w", err)
		}

		edited, err := os.ReadFile(tmpPath)
		if err != nil {
			return err
		}

		var fields map[string]any
		if err := json.Unmarshal(edited, &fields); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}

		patchResp, err := client.patch("/profile", fields)
		if err != nil {
			return err
		}
		defer patchResp.Body.Close()

		if patchResp.StatusCode >= 400 {
			return fmt.Errorf("server returned %d", patchResp.StatusCode)
		}

		printSuccess("Profile updated")
		return nil
	},
}

func init() {
	profileCmd.AddCommand(profileShowCmd)
	profileCmd.AddCommand(profileSetCmd)
	profileCmd.AddCommand(profileEditCmd)
}

// --- recall ---

var recallCmd = &cobra.Command{
	Use:   "recall <query>",
	Short: "Semantic search over the knowledge base",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		query := strings.Join(args, " ")
		limit, _ := cmd.Flags().GetInt("limit")

		client, err := newAPIClient()
		if err != nil {
			return err
		}

		path := fmt.Sprintf("/recall?q=%s&limit=%d", query, limit)
		resp, err := client.get(path)
		if err != nil {
			return err
		}

		var results []struct {
			ID         string  `json:"id"`
			SourceID   string  `json:"source_id"`
			SourceType string  `json:"source_type"`
			Text       string  `json:"text"`
			Score      float32 `json:"score"`
			Tags       string  `json:"tags"`
		}
		if err := decodeJSON(resp, &results); err != nil {
			return err
		}

		if len(results) == 0 {
			fmt.Println("No results found.")
			return nil
		}

		for i, r := range results {
			fmt.Printf("\n%s [score: %.3f]\n", colorize(colorBold, fmt.Sprintf("Result %d", i+1)), r.Score)
			if r.Tags != "" && r.Tags != "[]" {
				fmt.Printf("  Tags: %s\n", r.Tags)
			}
			text := r.Text
			if len(text) > 500 {
				text = text[:500] + "..."
			}
			fmt.Printf("  %s\n", text)
		}
		return nil
	},
}

func init() {
	recallCmd.Flags().Int("limit", 5, "maximum number of results")
}

// --- interactions ---

var interactionsCmd = &cobra.Command{
	Use:   "interactions",
	Short: "Manage interaction history",
}

var interactionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent interactions",
	RunE: func(cmd *cobra.Command, args []string) error {
		limit, _ := cmd.Flags().GetInt("limit")

		client, err := newAPIClient()
		if err != nil {
			return err
		}

		path := fmt.Sprintf("/interactions?limit=%d", limit)
		resp, err := client.get(path)
		if err != nil {
			return err
		}

		var interactions []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
			UserQuery string `json:"user_query"`
			Status    string `json:"status"`
		}
		if err := decodeJSON(resp, &interactions); err != nil {
			return err
		}

		if len(interactions) == 0 {
			fmt.Println("No interactions found.")
			return nil
		}

		for _, ix := range interactions {
			query := ix.UserQuery
			if len(query) > 80 {
				query = query[:80] + "..."
			}
			fmt.Printf("%s  %s  %s\n",
				colorize(colorCyan, ix.ID[:8]),
				ix.CreatedAt,
				query,
			)
		}
		return nil
	},
}

var interactionsShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show a single interaction",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		client, err := newAPIClient()
		if err != nil {
			return err
		}

		resp, err := client.get("/interactions/" + args[0])
		if err != nil {
			return err
		}

		var interaction any
		if err := decodeJSON(resp, &interaction); err != nil {
			return err
		}

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(interaction)
	},
}

func init() {
	interactionsListCmd.Flags().Int("limit", 20, "maximum number of interactions to list")
	interactionsCmd.AddCommand(interactionsListCmd)
	interactionsCmd.AddCommand(interactionsShowCmd)
}

// --- data ---

var dataCmd = &cobra.Command{
	Use:   "data",
	Short: "Export or purge stored data",
}

var dataExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export all stored data as JSONL",
	RunE: func(cmd *cobra.Command, args []string) error {
		output, _ := cmd.Flags().GetString("output")

		client, err := newAPIClient()
		if err != nil {
			return err
		}

		var writer *os.File
		if output != "" {
			f, err := os.Create(output)
			if err != nil {
				return fmt.Errorf("creating output file: %w", err)
			}
			defer f.Close()
			writer = f
		} else {
			writer = os.Stdout
		}

		enc := json.NewEncoder(writer)

		// Export context docs.
		offset := 0
		for {
			resp, err := client.get(fmt.Sprintf("/context-docs?limit=100&offset=%d", offset))
			if err != nil {
				return err
			}
			var docs []any
			if err := decodeJSON(resp, &docs); err != nil {
				return err
			}
			if len(docs) == 0 {
				break
			}
			for _, doc := range docs {
				record := map[string]any{"type": "context_doc", "data": doc}
				enc.Encode(record)
			}
			offset += len(docs)
		}

		// Export interactions.
		offset = 0
		for {
			resp, err := client.get(fmt.Sprintf("/interactions?limit=100&offset=%d", offset))
			if err != nil {
				return err
			}
			var interactions []any
			if err := decodeJSON(resp, &interactions); err != nil {
				return err
			}
			if len(interactions) == 0 {
				break
			}
			for _, ix := range interactions {
				record := map[string]any{"type": "interaction", "data": ix}
				enc.Encode(record)
			}
			offset += len(interactions)
		}

		if output != "" {
			printSuccess("Data exported to %s", output)
		}
		return nil
	},
}

var dataPurgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Delete all stored data",
	RunE: func(cmd *cobra.Command, args []string) error {
		confirm, _ := cmd.Flags().GetBool("confirm")
		if !confirm {
			printWarning("This will delete ALL stored data. Use --confirm to proceed.")
			return nil
		}

		client, err := newAPIClient()
		if err != nil {
			return err
		}

		// Delete all context docs.
		printStep("Deleting context docs...")
		for {
			resp, err := client.get("/context-docs?limit=100")
			if err != nil {
				return err
			}
			var docs []struct {
				ID string `json:"id"`
			}
			if err := decodeJSON(resp, &docs); err != nil {
				return err
			}
			if len(docs) == 0 {
				break
			}
			for _, doc := range docs {
				if _, err := client.delete("/context-docs/" + doc.ID); err != nil {
					printError("Failed to delete doc %s: %v", doc.ID, err)
				}
			}
		}

		// Delete all interactions.
		printStep("Deleting interactions...")
		for {
			resp, err := client.get("/interactions?limit=100")
			if err != nil {
				return err
			}
			var interactions []struct {
				ID string `json:"id"`
			}
			if err := decodeJSON(resp, &interactions); err != nil {
				return err
			}
			if len(interactions) == 0 {
				break
			}
			for _, ix := range interactions {
				if _, err := client.delete("/interactions/" + ix.ID); err != nil {
					printError("Failed to delete interaction %s: %v", ix.ID, err)
				}
			}
		}

		printSuccess("All data purged")
		return nil
	},
}

func init() {
	dataExportCmd.Flags().String("output", "", "output file path (default: stdout)")
	dataPurgeCmd.Flags().Bool("confirm", false, "confirm data purge")
	dataCmd.AddCommand(dataExportCmd)
	dataCmd.AddCommand(dataPurgeCmd)
}

// --- config ---

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show or update configuration",
}

var configShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show current configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return err
		}

		keys := config.ShowAll(cfg)
		for _, k := range keys {
			fmt.Printf("  %s = %s\n", colorize(colorBold, k.Key), k.Value)
		}
		return nil
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set a configuration value",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		key, value := args[0], args[1]

		if err := config.SetKey(key, value); err != nil {
			return err
		}

		printSuccess("Set %s = %s", key, value)
		return nil
	},
}

func init() {
	configCmd.AddCommand(configShowCmd)
	configCmd.AddCommand(configSetCmd)
}
