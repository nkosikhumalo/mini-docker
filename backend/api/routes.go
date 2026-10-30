package api

import (
	"github.com/gin-gonic/gin"

	"foxvue-api/db"
	"foxvue-api/storage"
)

func RegisterRoutes(r *gin.Engine, store storage.SessionStore, database *db.DB) {
	userRepo := db.NewUserRepo(database.DB)
	sessionRepo := db.NewSessionRepo(database.DB)
	apiKeyRepo := db.NewAPIKeyRepo(database.DB)
	quotaRepo := db.NewQuotaRepo(database.DB)
	verifRepo := db.NewVerificationRepo(database.DB)
	trialRepo := db.NewTrialRepo(database.DB)

	// Clean up expired verifications at startup
	_ = verifRepo.DeleteExpired()

	authH := newAuthHandler(userRepo, verifRepo)
	resetH := newResetHandler(userRepo)
	oauthH := newOAuthHandler(userRepo)
	interviewH := newInterviewHandler(store, sessionRepo, apiKeyRepo, quotaRepo)
	apiKeyH := newAPIKeyHandler(apiKeyRepo)
	transcribeH := newTranscribeHandler(apiKeyRepo)
	quotaH := newQuotaHandler(quotaRepo)
	trialH := newTrialHandler(trialRepo)

	// ── Public ──────────────────────────────────────────────────────────────
	r.POST("/api/auth/signup", authH.signup)
	r.POST("/api/auth/check-code", authH.checkCode)
	r.POST("/api/auth/complete-registration", authH.completeRegistration)
	r.POST("/api/auth/verify-email", authH.completeRegistration) // alias
	r.POST("/api/auth/resend-verification", authH.resendVerification)
	r.POST("/api/auth/login", authH.login)
	r.POST("/api/auth/forgot-password", resetH.forgotPassword)
	r.POST("/api/auth/reset-password", resetH.resetPassword)

	r.GET("/api/trial/status", trialH.status)
	r.GET("/api/question", getQuestion)

	// ── OAuth ────────────────────────────────────────────────────────────────
	r.GET("/auth/:provider", oauthH.redirect)
	r.GET("/auth/:provider/callback", oauthH.callback)

	// ── Protected — any valid JWT ────────────────────────────────────────────
	auth := r.Group("/")
	auth.Use(RequireAuth)

	auth.GET("/api/auth/me", authH.me)
	auth.GET("/api/quota", quotaH.get)

	auth.POST("/api/apikeys", apiKeyH.save)
	auth.GET("/api/apikeys", apiKeyH.list)
	auth.POST("/api/apikeys/:id/test", apiKeyH.test)
	auth.POST("/api/apikeys/:id/activate", apiKeyH.activate)
	auth.DELETE("/api/apikeys/:id", apiKeyH.delete)

	// ── Interview routes — TrialMiddleware allows both guests and JWT users ──
	// Guests get trial enforcement; JWT users pass straight through.
	trial := r.Group("/")
	trial.Use(TrialMiddleware(trialRepo))

	trial.POST("/api/transcribe", transcribeH.transcribeAudio)
	trial.POST("/api/interview/generate-questions", interviewH.generateQuestions)
	trial.POST("/api/interview/evaluate-answer", interviewH.evaluateAnswer)
	trial.GET("/api/ws", handleWebSocket)

	// ── Authenticated-only interview routes (history requires an account) ───
	interview := r.Group("/")
	interview.Use(RequireInterviewAuth)

	interview.GET("/api/interview/sessions", interviewH.getAllSessions)
	interview.GET("/api/interview/history", interviewH.getHistory)
	interview.DELETE("/api/interview/sessions/:sessionId", interviewH.deleteSession)
}
