package catalog

import (
	"context"
	"encoding/json"

	"github.com/the-agency-labs/encompass/server/internal/catalog/mappings"
	"github.com/the-agency-labs/encompass/server/internal/providers"
	"github.com/the-agency-labs/encompass/server/internal/providers/anilist"
	"github.com/the-agency-labs/encompass/server/internal/providers/tvdb"
)

type (
	// Metadata is a provider's metadata map (see providers.Metadata).
	Metadata = providers.Metadata
	// SearchPage is a provider's search response.
	SearchPage = providers.SearchPage
)

// The interfaces below are the slices of each provider client the catalog
// uses. The real clients satisfy them; tests pass fakes.

// TMDB is the TMDB client.
type TMDB interface {
	TV(ctx context.Context, id string) (Metadata, error)
	Movie(ctx context.Context, id string) (Metadata, error)
	TVWithSeasons(ctx context.Context, id string, seasons []int) (Metadata, error)
	Season(ctx context.Context, id string, season int) (Metadata, error)
	Search(ctx context.Context, mediaType, query string, page int, showAdult bool) (SearchPage, error)
	Find(ctx context.Context, externalID, externalSource string) (map[string]any, error)
	TVChanges(ctx context.Context) (map[string]struct{}, error)
	MovieChanges(ctx context.Context) (map[string]struct{}, error)
}

// TVDB is the TheTVDB client: the source of every show (tv and anime).
type TVDB interface {
	Series(ctx context.Context, id string) (Metadata, error)
	Seasons(ctx context.Context, id string) ([]tvdb.Season, error)
	Season(ctx context.Context, id string, number int) (Metadata, error)
	Search(ctx context.Context, query string, page int) (SearchPage, error)
	Characters(ctx context.Context, id string) ([]tvdb.Character, error)
	PersonRemoteIDs(ctx context.Context, personID string) (map[string]string, error)
	FindByRemoteID(ctx context.Context, remoteID string) (*tvdb.Hit, error)
	Episode(ctx context.Context, episodeID string) (Metadata, error)
}

// AniList is the AniList client.
type AniList interface {
	Anime(ctx context.Context, id string) (Metadata, error)
	Manga(ctx context.Context, id string) (Metadata, error)
	Search(ctx context.Context, mediaType, query string, page int, showAdult bool) (SearchPage, error)
	EpisodeMedia(ctx context.Context, source, id string) anilist.EpisodeMedia
	Credits(ctx context.Context, source, id string) ([]anilist.VoiceCredit, error)
	GraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error)
}

// MAL is the MyAnimeList client.
type MAL interface {
	Anime(ctx context.Context, id string) (Metadata, error)
	Manga(ctx context.Context, id string) (Metadata, error)
	Search(ctx context.Context, mediaType, query string, page int, showAdult bool) (SearchPage, error)
}

// MangaUpdates is the MangaUpdates client.
type MangaUpdates interface {
	Manga(ctx context.Context, id string) (Metadata, error)
	Search(ctx context.Context, query string, page int, showAdult bool) (SearchPage, error)
}

// Hardcover is the Hardcover client.
type Hardcover interface {
	Book(ctx context.Context, id string) (Metadata, error)
	Search(ctx context.Context, query string, page int) (SearchPage, error)
}

// OpenLibrary is the Open Library client.
type OpenLibrary interface {
	Book(ctx context.Context, id string) (Metadata, error)
	Search(ctx context.Context, query string, page int) (SearchPage, error)
}

// ComicVine is the Comic Vine client.
type ComicVine interface {
	Comic(ctx context.Context, id string) (Metadata, error)
	Issue(ctx context.Context, id string) (Metadata, error)
	Search(ctx context.Context, query string, page int) (SearchPage, error)
}

// IGDB is the IGDB client.
type IGDB interface {
	Game(ctx context.Context, id string) (Metadata, error)
	Search(ctx context.Context, query string, page int, showAdult bool) (SearchPage, error)
}

// BGG is the BoardGameGeek client.
type BGG interface {
	Boardgame(ctx context.Context, id string) (Metadata, error)
	Search(ctx context.Context, query string, page int) (SearchPage, error)
}

// Mappings is the anime mapping store (*mappings.Store).
type Mappings interface {
	Lookup(ctx context.Context, source, mediaID string) (*mappings.Mapping, error)
	ByTMDBTV(ctx context.Context, id int64) ([]mappings.Mapping, error)
	ByTMDBMovie(ctx context.Context, id int64) ([]mappings.Mapping, error)
	ByTVDB(ctx context.Context, id int64) ([]mappings.Mapping, error)
	AnimeTMDBIDs(ctx context.Context) (mappings.TMDBIDs, error)
}

// Providers bundles the provider clients. A nil field means that provider is
// not configured and its (kind, source) pairs return ErrUnsupported.
type Providers struct {
	TMDB         TMDB
	TVDB         TVDB
	AniList      AniList
	MAL          MAL
	MangaUpdates MangaUpdates
	Hardcover    Hardcover
	OpenLibrary  OpenLibrary
	ComicVine    ComicVine
	IGDB         IGDB
	BGG          BGG
}
