package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	_ "github.com/jinzhu/gorm/dialects/sqlite"
	"github.com/joho/godotenv"
	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/paymentintent"
	"github.com/stripe/stripe-go/v72/webhook"
)

type Payment struct {
	gorm.Model
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	PaymentID     string `json:"payment_id"`
	PaymentStatus string `json:"payment_status"`
}

func initDB() *gorm.DB {
	db, err := gorm.Open("sqlite3", "payments.db")
	if err != nil {
		log.Fatal("failed to connect to database", err)
	}

	db.AutoMigrate(&Payment{})
	return db
}

func createPaymentIntent(c *gin.Context) {
	var payment Payment
	if err := c.BindJSON(&payment); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")

	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(payment.Amount),
		Currency: stripe.String(payment.Currency),
	}
	pi, err := paymentintent.New(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	payment.PaymentID = pi.ID
	payment.PaymentStatus = string(pi.Status)
	db := c.MustGet("db").(*gorm.DB)
	db.Create(&payment)

	c.JSON(http.StatusOK, gin.H{"client_secret": pi.ClientSecret})
}

func handleStripeWebhook(c *gin.Context) {
	const MaxBodyBytes = int64(65536)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)
	payload, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": err.Error()})
		return
	}

	stripeWebhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	event, err := webhook.ConstructEvent(payload, c.Request.Header.Get("Stripe-Signature"), stripeWebhookSecret)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	db := c.MustGet("db").(*gorm.DB)

	if event.Type == "payment_intent.succeeded" {
		var paymentIntent stripe.PaymentIntent
		err := json.Unmarshal(event.Data.Raw, &paymentIntent)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		var payment Payment
		if db.Where("payment_id = ?", paymentIntent.ID).First(&payment).RecordNotFound() {
			c.JSON(http.StatusNotFound, gin.H{"error": "Payment not found"})
			return
		}

		payment.PaymentStatus = "succeeded"
		db.Save(&payment)
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func main() {
	// Load environment variables from .env file
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	db := initDB()
	defer db.Close()

	router := gin.Default()

	// Middleware to pass database connection to handlers
	router.Use(func(c *gin.Context) {
		c.Set("db", db)
		c.Next()
	})

	router.POST("/create-payment-intent", createPaymentIntent)
	router.POST("/webhook", handleStripeWebhook)

	router.Run(":8080")
}
