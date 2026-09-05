package Explore

import (
	"vx-api/Config"
)

func SeedSounds() {
	sounds := []Sound{
		{
			Title:        "Summer Vibes",
			AuthorName:   "Chill Master",
			AudioURL:     "https://www.soundhelix.com/examples/mp3/SoundHelix-Song-1.mp3",
			AuthorAvatar: "https://api.dicebear.com/7.x/identicon/svg?seed=Summer",
			TotalVideos:  150,
		},
		{
			Title:        "Midnight Drive",
			AuthorName:   "Synth Boy",
			AudioURL:     "https://www.soundhelix.com/examples/mp3/SoundHelix-Song-2.mp3",
			AuthorAvatar: "https://api.dicebear.com/7.x/identicon/svg?seed=Midnight",
			TotalVideos:  89,
		},
		{
			Title:        "Happy Days",
			AuthorName:   "Upbeat Crew",
			AudioURL:     "https://www.soundhelix.com/examples/mp3/SoundHelix-Song-3.mp3",
			AuthorAvatar: "https://api.dicebear.com/7.x/identicon/svg?seed=Happy",
			TotalVideos:  234,
		},
	}

	for _, s := range sounds {
		var count int64
		Config.DB.Model(&Sound{}).Where("title = ?", s.Title).Count(&count)
		if count == 0 {
			Config.DB.Create(&s)
		}
	}
}
