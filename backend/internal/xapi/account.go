package xapi

import (
	"context"
	"fmt"
	"strings"

	"trex/backend/internal/model"
)

func (c *Client) Account(ctx context.Context, screenName string) (model.AccountRecord, error) {
	screenName = cleanScreenName(screenName)
	if screenName == "" {
		return model.AccountRecord{}, fmt.Errorf("username is required")
	}
	profile, _, err := c.Do(ctx, "UserByScreenName", map[string]any{
		"screen_name": screenName, "withGrokTranslatedBio": true,
	}, userFeatures, map[string]any{"withPayments": false, "withAuxiliaryUserLabels": true}, "https://x.com/"+screenName)
	if err != nil {
		return model.AccountRecord{}, err
	}
	about, _, aboutErr := c.Do(ctx, "AboutAccountQuery", map[string]any{
		"screenName": screenName,
	}, nil, nil, "https://x.com/"+screenName+"/about")
	if aboutErr != nil {
		c.logger.Error("AboutAccountQuery failed for @" + screenName + ": " + aboutErr.Error())
		about = map[string]any{}
	}
	return combineAccount(screenName, profile, about), nil
}

func combineAccount(screenName string, profile, about map[string]any) model.AccountRecord {
	user := mapPath(profile, "data", "user", "result")
	if len(user) == 0 {
		user = findMapByKeys(profile, "rest_id", "legacy")
	}
	legacy := asMap(user["legacy"])
	core := asMap(user["core"])
	privacy := asMap(user["privacy"])
	verification := asMap(user["verification"])
	verificationInfo := asMap(user["verification_info"])
	relationship := asMap(user["relationship_perspectives"])
	profileBio := asMap(user["profile_bio"])
	description := asMap(profileBio["description"])
	location := asMap(user["location"])
	professional := asMap(user["professional"])
	tipjar := asMap(user["tipjar_settings"])
	aboutUser := mapPath(about, "data", "user_result_by_screen_name", "result")
	aboutProfile := asMap(aboutUser["about_profile"])
	usernameChanges := asMap(aboutProfile["username_changes"])

	name := firstString(core["name"], legacy["name"])
	handle := firstString(core["screen_name"], legacy["screen_name"], screenName)
	avatar := firstString(pathValue(user, "avatar", "image_url"), legacy["profile_image_url_https"])
	fields := map[string]any{
		"screen_name":                         handle,
		"name":                                name,
		"rest_id":                             firstValue(user["rest_id"], aboutUser["rest_id"]),
		"profile_id":                          firstValue(user["id"], aboutUser["id"]),
		"created_at":                          firstValue(core["created_at"], legacy["created_at"]),
		"bio":                                 firstValue(description["text"], legacy["description"]),
		"bio_language":                        firstValue(description["lang"], user["profile_bio_language"]),
		"avatar_url":                          avatar,
		"profile_banner_url":                  legacy["profile_banner_url"],
		"profile_image_shape":                 firstValue(user["profile_image_shape"], aboutUser["profile_image_shape"]),
		"protected":                           firstValue(privacy["protected"], legacy["protected"]),
		"location":                            firstValue(location["location"], legacy["location"]),
		"url":                                 pathValue(legacy, "entities", "url", "urls", 0, "url"),
		"expanded_url":                        pathValue(legacy, "entities", "url", "urls", 0, "expanded_url"),
		"display_url":                         pathValue(legacy, "entities", "url", "urls", 0, "display_url"),
		"default_profile":                     legacy["default_profile"],
		"default_profile_image":               legacy["default_profile_image"],
		"is_profile_translatable":             user["is_profile_translatable"],
		"possibly_sensitive":                  legacy["possibly_sensitive"],
		"parody_commentary_fan_label":         user["parody_commentary_fan_label"],
		"followers_count":                     legacy["followers_count"],
		"normal_followers_count":              legacy["normal_followers_count"],
		"fast_followers_count":                legacy["fast_followers_count"],
		"following_count":                     legacy["friends_count"],
		"statuses_count":                      legacy["statuses_count"],
		"media_count":                         legacy["media_count"],
		"likes_count":                         legacy["favourites_count"],
		"listed_count":                        legacy["listed_count"],
		"creator_subscriptions_count":         user["creator_subscriptions_count"],
		"highlighted_tweets":                  user["highlighted_tweets"],
		"can_highlight_tweets":                user["can_highlight_tweets"],
		"pinned_tweet_ids":                    legacy["pinned_tweet_ids_str"],
		"account_based_in":                    aboutProfile["account_based_in"],
		"source":                              aboutProfile["source"],
		"created_country_accurate":            aboutProfile["created_country_accurate"],
		"location_accurate":                   aboutProfile["location_accurate"],
		"learn_more_url":                      aboutProfile["learn_more_url"],
		"verified":                            firstValue(verification["verified"], legacy["verified"]),
		"is_blue_verified":                    firstValue(user["is_blue_verified"], aboutUser["is_blue_verified"]),
		"is_identity_verified":                firstValue(verificationInfo["is_identity_verified"], pathValue(aboutUser, "verification_info", "is_identity_verified")),
		"verified_since_msec":                 firstValue(pathValue(verificationInfo, "reason", "verified_since_msec"), pathValue(aboutUser, "verification_info", "reason", "verified_since_msec")),
		"verified_reason":                     pathValue(verificationInfo, "reason", "description", "text"),
		"verified_phone_status":               user["verified_phone_status"],
		"can_dm":                              relationship["can_dm"],
		"can_media_tag":                       relationship["can_media_tag"],
		"following":                           relationship["following"],
		"followed_by":                         relationship["followed_by"],
		"blocking":                            relationship["blocking"],
		"blocked_by":                          relationship["blocked_by"],
		"muting":                              relationship["muting"],
		"notifications":                       relationship["notifications_enabled"],
		"want_retweets":                       relationship["want_retweets"],
		"needs_phone_verification":            user["needs_phone_verification"],
		"has_graduated_access":                user["has_graduated_access"],
		"has_hidden_subscriptions_on_profile": user["has_hidden_subscriptions_on_profile"],
		"has_custom_timelines":                user["has_custom_timelines"],
		"is_translator":                       user["is_translator"],
		"translator_type":                     user["translator_type"],
		"professional_type":                   professional["professional_type"],
		"professional_category":               pathValue(professional, "category", 0, "name"),
		"professional_rest_id":                professional["rest_id"],
		"premium_gifting_eligible":            user["premium_gifting_eligible"],
		"super_follow_eligible":               user["super_follow_eligible"],
		"super_followed_by":                   relationship["super_followed_by"],
		"super_following":                     relationship["super_following"],
		"username_changes_count":              usernameChanges["count"],
		"username_last_changed_at_msec":       usernameChanges["last_changed_at_msec"],
		"tipjar_settings":                     tipjar,
	}
	sections := accountSections(fields)
	return model.AccountRecord{
		ScreenName: handle, Name: name, AvatarURL: avatar,
		Fields: fields, Sections: sections, RawProfile: profile, RawAbout: about,
	}
}

func accountSections(fields map[string]any) []model.Section {
	definitions := []struct {
		title string
		keys  []string
	}{
		{"Identity", []string{"screen_name", "name", "rest_id", "profile_id", "created_at", "bio", "bio_language", "avatar_url", "profile_banner_url", "profile_image_shape", "protected"}},
		{"Profile", []string{"location", "url", "expanded_url", "display_url", "default_profile", "default_profile_image", "is_profile_translatable", "possibly_sensitive", "parody_commentary_fan_label"}},
		{"Audience & Activity", []string{"followers_count", "normal_followers_count", "fast_followers_count", "following_count", "statuses_count", "media_count", "likes_count", "listed_count", "creator_subscriptions_count", "highlighted_tweets", "can_highlight_tweets", "pinned_tweet_ids"}},
		{"Account Location", []string{"account_based_in", "source", "created_country_accurate", "location_accurate", "learn_more_url"}},
		{"Verification", []string{"verified", "is_blue_verified", "is_identity_verified", "verified_since_msec", "verified_reason", "verified_phone_status"}},
		{"Permissions & Relationship", []string{"can_dm", "can_media_tag", "following", "followed_by", "blocking", "blocked_by", "muting", "notifications", "want_retweets", "needs_phone_verification", "has_graduated_access", "has_hidden_subscriptions_on_profile", "has_custom_timelines", "is_translator", "translator_type"}},
		{"Professional", []string{"professional_type", "professional_category", "professional_rest_id", "premium_gifting_eligible", "super_follow_eligible", "super_followed_by", "super_following"}},
		{"Username History", []string{"username_changes_count", "username_last_changed_at_msec"}},
	}
	sections := make([]model.Section, 0, len(definitions))
	for _, definition := range definitions {
		rows := []model.Field{}
		for _, key := range definition.keys {
			if value := fields[key]; hasValue(value) {
				rows = append(rows, model.Field{Label: humanize(key), Value: value})
			}
		}
		if len(rows) > 0 {
			sections = append(sections, model.Section{Title: definition.title, Rows: rows})
		}
	}
	return sections
}

func cleanScreenName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "https://x.com/")
	value = strings.TrimPrefix(value, "https://twitter.com/")
	value = strings.TrimLeft(value, "@")
	value = strings.Split(value, "/")[0]
	value = strings.Split(value, "?")[0]
	return value
}
