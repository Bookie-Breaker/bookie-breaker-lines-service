package model

type League string

const (
	LeagueNFL     League = "NFL"
	LeagueNBA     League = "NBA"
	LeagueMLB     League = "MLB"
	LeagueNCAAFB  League = "NCAA_FB"
	LeagueNCAACB  League = "NCAA_BB"
	LeagueNCAACBB League = "NCAA_BSB"
)

type MarketType string

const (
	MarketSpread     MarketType = "SPREAD"
	MarketTotal      MarketType = "TOTAL"
	MarketMoneyline  MarketType = "MONEYLINE"
	MarketPlayerProp MarketType = "PLAYER_PROP"
	MarketTeamProp   MarketType = "TEAM_PROP"
	MarketGameProp   MarketType = "GAME_PROP"
	MarketFuture     MarketType = "FUTURE"
	MarketLive       MarketType = "LIVE"
)

type Sport string

const (
	SportFootball   Sport = "FOOTBALL"
	SportBasketball Sport = "BASKETBALL"
	SportBaseball   Sport = "BASEBALL"
)
