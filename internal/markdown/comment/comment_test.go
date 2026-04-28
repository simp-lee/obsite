package comment

import "testing"

func TestStrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "inline comment",
			input: "before %%hidden%% after\n",
			want:  "before  after\n",
		},
		{
			name:  "multiline comment preserves line structure",
			input: "before\n%%hidden\nstill hidden%%\nafter\n",
			want:  "before\n\n\nafter\n",
		},
		{
			name:  "code fence content is preserved",
			input: "before\n```md\n%% not a comment %%\n```\nafter %%gone%%\n",
			want:  "before\n```md\n%% not a comment %%\n```\nafter \n",
		},
		{
			name:  "indented code block content is preserved",
			input: "before\n\n    %% not a comment %%\nafter %%gone%%\n",
			want:  "before\n\n    %% not a comment %%\nafter \n",
		},
		{
			name:  "indented paragraph continuation is still stripped",
			input: "before\n    %%gone%%\nafter\n",
			want:  "before\n    \nafter\n",
		},
		{
			name:  "blockquote fenced code block ends with container",
			input: "> ```md\n> %% not a comment %%\nafter %%gone%%\n",
			want:  "> ```md\n> %% not a comment %%\nafter \n",
		},
		{
			name:  "blockquote indented code block content is preserved",
			input: "> quote\n>\n>     %% not a comment %%\nafter %%gone%%\n",
			want:  "> quote\n>\n>     %% not a comment %%\nafter \n",
		},
		{
			name:  "list fenced code block content is preserved",
			input: "- item\n\n    ```md\n    %% not a comment %%\n    ```\nafter %%gone%%\n",
			want:  "- item\n\n    ```md\n    %% not a comment %%\n    ```\nafter \n",
		},
		{
			name:  "list fenced code block content is preserved with tab indentation",
			input: "- item\n\n\t```md\n\t%% not a comment %%\n\t```\nafter %%gone%%\n",
			want:  "- item\n\n\t```md\n\t%% not a comment %%\n\t```\nafter \n",
		},
		{
			name:  "list indented code block content is preserved",
			input: "- item\n\n      %% not a comment %%\nafter %%gone%%\n",
			want:  "- item\n\n      %% not a comment %%\nafter \n",
		},
		{
			name:  "list indented code block content is preserved with tab indentation",
			input: "- item\n\n\t\t%% not a comment %%\nafter %%gone%%\n",
			want:  "- item\n\n\t\t%% not a comment %%\nafter \n",
		},
		{
			name:  "nested list blockquote fenced code block content is preserved with tab indentation",
			input: "- item\n\n\t> ```md\n\t> %% not a comment %%\n\t> ```\nafter %%gone%%\n",
			want:  "- item\n\n\t> ```md\n\t> %% not a comment %%\n\t> ```\nafter \n",
		},
		{
			name:  "fence opener revealed after stripping same line comment",
			input: "before\n%%gone%%```md\n%% not a comment %%\n```\nafter %%gone%%\n",
			want:  "before\n```md\n%% not a comment %%\n```\nafter \n",
		},
		{
			name:  "indented opener revealed after stripping same line comment",
			input: "before\n\n%%gone%%    code\n    %% not a comment %%\nafter %%gone%%\n",
			want:  "before\n\n    code\n    %% not a comment %%\nafter \n",
		},
		{
			name:  "closing fence line strips trailing comment before closing",
			input: "before\n```md\n%% not a comment %%\n```%%gone%%\nafter %%gone%%\n",
			want:  "before\n```md\n%% not a comment %%\n```\nafter \n",
		},
		{
			name:  "comment mixed with surrounding prose",
			input: "alpha %%hidden\nstill hidden%% omega\nbeta\n",
			want:  "alpha \n omega\nbeta\n",
		},
		{
			name:  "inline code literal percent pairs stay visible",
			input: "before `%%literal%%` after %%gone%%\n",
			want:  "before `%%literal%%` after \n",
		},
		{
			name:  "inline raw html literal percent pairs stay visible",
			input: "before <span>%%literal%%</span> after %%gone%%\n",
			want:  "before <span>%%literal%%</span> after \n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := string(Strip([]byte(tt.input)))
			if got != tt.want {
				t.Fatalf("Strip() = %q, want %q", got, tt.want)
			}
		})
	}
}
