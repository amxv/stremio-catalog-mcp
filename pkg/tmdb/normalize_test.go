package tmdb

import "testing"

func TestNormalizeInternationalTitles(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"Amelie", "Amélie"},
		{"Shogun", "Shōgun"},
		{"Leon The Professional", "Léon: The Professional"},
		{"卧虎藏龙", "臥虎藏龍"},
		{"12 Monkeys", "Twelve Monkeys"},
		{"Spider-Man 3", "Spider Man 3"},
		{"  THE  DARK  KNIGHT  ", "The Dark Knight"},
	} {
		if !sameTitle(tc.a, tc.b) {
			t.Errorf("expected title equivalence %q ~ %q (got %q, %q)", tc.a, tc.b, normalize(tc.a), normalize(tc.b))
		}
	}
	for _, tc := range []struct{ a, b string }{
		{"Alien", "Alien³"},
		{"Alien", "Alien: Covenant"},
		{"Dr. Strangelove", "Dr. Strange"},
		{"The Office", "The Office: Superfan Episodes"},
	} {
		if sameTitle(tc.a, tc.b) {
			t.Errorf("not exact: %q vs %q", tc.a, tc.b)
		}
	}
	if relaxedKey("Baahubali: The Beginning") != relaxedKey("Bāhubali: The Beginning") {
		t.Fatal("long-A transliteration not recognized")
	}
	if relaxedKey("Intouchables") != relaxedKey("The Intouchables") {
		t.Fatal("optional leading article not normalized")
	}
	if !shortTitle("Dr. Strangelove", "Dr. Strangelove or: How I Learned to Stop Worrying and Love the Bomb") {
		t.Fatal("shortened title should match a full prefix at a word boundary")
	}
	if shortTitle("Alien", "Alien³") {
		t.Fatal("sequel number must not be treated as a title prefix")
	}
}

func TestParseQualifiers(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  titleQuery
	}{
		{"Dune (2021)", titleQuery{Name: "Dune", Year: 2021}},
		{"Oldboy (2003)", titleQuery{Name: "Oldboy", Year: 2003}},
		{"The Office (US)", titleQuery{Name: "The Office", Country: "US"}},
		{"The Office (UK)", titleQuery{Name: "The Office", Country: "GB"}},
		{"Avatar (2009) (movie)", titleQuery{Name: "Avatar", Year: 2009, Kind: "movie"}},
		{"12 Monkeys (2015) (series)", titleQuery{Name: "12 Monkeys", Year: 2015, Kind: "tv"}},
		{"Spirited Away (anime)", titleQuery{Name: "Spirited Away", Anime: true}},
		{"(500) Days of Summer", titleQuery{Name: "(500) Days of Summer"}},
		{"Unknown (Redux)", titleQuery{Name: "Unknown (Redux)"}},
	} {
		if got := parseTitleQuery(tc.input); got != tc.want {
			t.Errorf("%q parsed as %+v; want %+v", tc.input, got, tc.want)
		}
	}
}

func TestNonLatinExactTitleIncludesTranslatedOriginalTitle(t *testing.T) {
	if !hasNonLatinScript("기생충") || !hasNonLatinScript("卧虎藏龙") || hasNonLatinScript("León") {
		t.Fatal("script classification should include Korean and Chinese, but not Latin")
	}
	hits := []searchHit{
		{ID: 1, MediaType: "tv", Name: "기생충", VoteCount: 10},
		{ID: 2, MediaType: "movie", Title: "Parasite", OriginalTitle: "기생충", VoteCount: 1000},
	}
	found := selectCandidates(hits, titleQuery{Name: "기생충"})
	if len(found) != 2 || found[0].ID != 2 {
		t.Fatalf("non-Latin originals should be retained and ranked: %+v", found)
	}
}
