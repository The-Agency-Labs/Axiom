package anilist

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	// MaxCharacters caps the characters fetched per anime (src/catalog/credits.py).
	MaxCharacters = 50
	castPageSize  = 25
)

const castQuery = `
query ($id: Int, $idMal: Int, $page: Int) {
  Media(id: $id, idMal: $idMal, type: ANIME) {
    characters(sort: [ROLE, RELEVANCE, ID], page: $page, perPage: 25) {
      pageInfo { hasNextPage }
      edges {
        role
        node { id name { full native } image { medium } }
        voiceActors(sort: [RELEVANCE, ID]) {
          id languageV2 name { full native } image { medium }
        }
      }
    }
  }
}
`

// VoiceCredit is one voice actor playing one character. Order is the
// character's position (several actors of one character share it).
type VoiceCredit struct {
	PersonID       int
	PersonName     string
	PersonNative   string
	PersonImage    string
	Character      string
	CharacterImage string
	CharacterRole  string
	Language       string // lower case: "japanese" or "english"
	Order          int
}

// Credits fetches the characters of an anime with their Japanese and
// English voice actors, up to MaxCharacters. source is "anilist" or "mal".
// Storage (people/credits rows, field length limits) is the caller's job.
func (c *Client) Credits(ctx context.Context, source, mediaID string) ([]VoiceCredit, error) {
	id, err := strconv.Atoi(strings.TrimSpace(mediaID))
	if err != nil {
		return nil, fmt.Errorf("anilist: invalid media id %q", mediaID)
	}
	key := "idMal"
	if source == Source {
		key = "id"
	}
	var credits []VoiceCredit
	order := 0
	for page := 1; page <= MaxCharacters/castPageSize; page++ {
		data, err := c.GraphQL(ctx, castQuery, map[string]any{key: id, "page": page})
		if err != nil {
			return nil, err
		}
		var resp struct {
			Media *struct {
				Characters *struct {
					PageInfo struct {
						HasNextPage bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					Edges []struct {
						Role string `json:"role"`
						Node struct {
							Name struct {
								Full string `json:"full"`
							} `json:"name"`
							Image *struct {
								Medium *string `json:"medium"`
							} `json:"image"`
						} `json:"node"`
						VoiceActors []struct {
							ID         int     `json:"id"`
							LanguageV2 *string `json:"languageV2"`
							Name       struct {
								Full   string  `json:"full"`
								Native *string `json:"native"`
							} `json:"name"`
							Image *struct {
								Medium *string `json:"medium"`
							} `json:"image"`
						} `json:"voiceActors"`
					} `json:"edges"`
				} `json:"characters"`
			} `json:"Media"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, err
		}
		if resp.Media == nil || resp.Media.Characters == nil {
			break
		}
		chars := resp.Media.Characters
		for _, edge := range chars.Edges {
			charImage := ""
			if edge.Node.Image != nil && edge.Node.Image.Medium != nil {
				charImage = *edge.Node.Image.Medium
			}
			for _, actor := range edge.VoiceActors {
				language := ""
				if actor.LanguageV2 != nil {
					language = strings.ToLower(*actor.LanguageV2)
				}
				if language != "japanese" && language != "english" {
					continue
				}
				vc := VoiceCredit{
					PersonID: actor.ID, PersonName: actor.Name.Full,
					Character: edge.Node.Name.Full, CharacterImage: charImage,
					CharacterRole: edge.Role, Language: language, Order: order,
				}
				if actor.Name.Native != nil {
					vc.PersonNative = *actor.Name.Native
				}
				if actor.Image != nil && actor.Image.Medium != nil {
					vc.PersonImage = *actor.Image.Medium
				}
				credits = append(credits, vc)
			}
			order++
		}
		if !chars.PageInfo.HasNextPage {
			break
		}
	}
	return credits, nil
}
