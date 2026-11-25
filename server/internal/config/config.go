// Package config reads settings from the environment. Names match the
// Django app's where a setting carries over, so the same .env keeps working.
// Every secret can also be given as NAME_FILE (Docker secrets).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds every setting the server reads.
type Config struct {
	Addr         string // listen address
	DatabasePath string
	SecretKey    string // signs session and CSRF cookies
	TimeZone     string
	BaseURLs     []string // public URLs, for absolute links and CSRF origin checks
	Registration bool

	TMDBKey      string
	TMDBLang     string
	TVDBKey      string
	MALKey       string
	IGDBID       string
	IGDBSecret   string
	BGGToken     string
	SteamKey     string
	HardcoverKey string
	ComicVineKey string
	MUNSFW       bool // MangaUpdates: include adult titles in search

	JellyfinURL            string
	JellyfinKey            string
	JellyfinUsers          []string // "jellyfin user:encompass user"
	JellyfinWatchedPercent int
	AniListUsers           []string // "anilist user:encompass user"

	SessionDays int
}

// Load reads the environment.
func Load() (Config, error) {
	c := Config{
		Addr:                   env("ADDR", ":8000"),
		DatabasePath:           env("DATABASE_PATH", "/data/encompass.db"),
		SecretKey:              secret("SECRET"),
		TimeZone:               env("TZ", "UTC"),
		BaseURLs:               list("URLS"),
		Registration:           boolean("REGISTRATION", false),
		TMDBKey:                secret("TMDB_API"),
		TMDBLang:               env("TMDB_LANG", "en"),
		TVDBKey:                secret("TVDB_API"),
		MALKey:                 secret("MAL_API"),
		IGDBID:                 secret("IGDB_ID"),
		IGDBSecret:             secret("IGDB_SECRET"),
		BGGToken:               secret("BGG_API_TOKEN"),
		SteamKey:               secret("STEAM_API_KEY"),
		HardcoverKey:           strings.TrimPrefix(secret("HARDCOVER_API"), "Bearer "),
		ComicVineKey:           secret("COMICVINE_API"),
		MUNSFW:                 boolean("MU_NSFW", false),
		JellyfinURL:            strings.TrimRight(env("JELLYFIN_URL", ""), "/"),
		JellyfinKey:            secret("JELLYFIN_API_KEY"),
		JellyfinUsers:          list("JELLYFIN_USERS"),
		AniListUsers:           list("ANILIST_USERS"),
		JellyfinWatchedPercent: integer("JELLYFIN_WATCHED_PERCENT", 75),
		SessionDays:            integer("SESSION_DAYS", 14),
	}
	if c.SecretKey == "" {
		return c, fmt.Errorf("SECRET (or SECRET_FILE) must be set")
	}
	return c, nil
}

func env(name, fallback string) string {
	if v, ok := os.LookupEnv(name); ok {
		return strings.TrimSpace(v)
	}
	return fallback
}

// secret reads NAME, or the file named by NAME_FILE.
func secret(name string) string {
	if v := env(name, ""); v != "" {
		return v
	}
	if path := env(name+"_FILE", ""); path != "" {
		if data, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	return ""
}

func list(name string) []string {
	var out []string
	for _, part := range strings.Split(env(name, ""), ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func boolean(name string, fallback bool) bool {
	v, err := strconv.ParseBool(env(name, strconv.FormatBool(fallback)))
	if err != nil {
		return fallback
	}
	return v
}

func integer(name string, fallback int) int {
	v, err := strconv.Atoi(env(name, strconv.Itoa(fallback)))
	if err != nil {
		return fallback
	}
	return v
}
