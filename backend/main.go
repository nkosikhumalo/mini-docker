package main

import (
	"log"
	"os"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"foxvue-api/api"
	"foxvue-api/db"
	"foxvue-api/storage"
)

func main() {
	// Load .env file (ignored in production where env vars are set directly)
	_ = godotenv.Load()

	// Connect to PostgreSQL
	database, err := db.Connect()
	if err != nil {
		log.Fatalf("[main] database error: %v", err)
	}

	r := gin.Default()

	// CORS — allow the React dev server
	frontendURL := os.Getenv("FRONTEND_URL")
	if frontendURL == "" {
		frontendURL = "http://localhost:5173"
	}
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{frontendURL}, // This matches your Azure Env Var
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "Accept"}, // Added "Accept"
		AllowCredentials: true,
	}))

	// DB-backed session store — survives restarts and scale-out events
	store := storage.NewSessionStore(database.DB)

	api.RegisterRoutes(r, store, database)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("[main] server starting on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("[main] server error: %v", err)
	}
}
