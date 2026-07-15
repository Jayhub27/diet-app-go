package config

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port              int
	SessionSecret     string
	DatabasePath      string
	DefaultAISource   string
	DefaultAIModel    string
	OpenAIKey         string
	OpenRouterKey     string
	OpenRouterAppURL  string
	OpenRouterAppName string
	OllamaBaseURL     string
	LMStudioBaseURL   string
	LocalModelPath    string
	YZMALibPath       string
	LocalModelsDir    string
	AllowAnonymous    bool
}

func Load() *Config {
	if err := godotenv.Load(); err != nil {
		log.Printf("config: no .env file loaded (%v)", err)
	}

	port, _ := strconv.Atoi(getenv("PORT", "8080"))
	allowAnon := getenv("ALLOW_ANONYMOUS", "true") == "true"

	secret := os.Getenv("SESSION_SECRET")
	if secret == "" {
		log.Println("WARNING: SESSION_SECRET is empty; using insecure dev fallback. Set it in production.")
		secret = "insecure-dev-secret-do-not-use-in-prod"
	}

	return &Config{
		Port:              port,
		SessionSecret:     secret,
		DatabasePath:      getenv("DATABASE_PATH", "./diet-app.db"),
		DefaultAISource:   getenv("DEFAULT_AI_SOURCE", "openrouter"),
		DefaultAIModel:    getenv("DEFAULT_AI_MODEL", "meta-llama/llama-3.3-70b-instruct:free"),
		OpenAIKey:         os.Getenv("OPENAI_API_KEY"),
		OpenRouterKey:     os.Getenv("OPENROUTER_API_KEY"),
		OpenRouterAppURL:  os.Getenv("OPENROUTER_APP_URL"),
		OpenRouterAppName: os.Getenv("OPENROUTER_APP_NAME"),
		OllamaBaseURL:     getenv("OLLAMA_BASE_URL", "http://localhost:11434/v1"),
		LMStudioBaseURL:   getenv("LMSTUDIO_BASE_URL", "http://localhost:1234/v1"),
		LocalModelPath:    os.Getenv("LOCAL_MODEL_PATH"),
		YZMALibPath:       os.Getenv("YZMA_LIB"),
		LocalModelsDir:    os.Getenv("LOCAL_MODELS_DIR"),
		AllowAnonymous:    allowAnon,
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
