package main

// InventoryItem represents a food item in the pantry.
type InventoryItem struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Quantity       float64 `json:"quantity"`
	Unit           string  `json:"unit"`
	Location       string  `json:"location"`
	ExpirationDate string  `json:"expiration_date"` // YYYY-MM-DD or ""
	LowThreshold   float64 `json:"low_threshold"`
	Barcode        string  `json:"barcode"`
}

// ShoppingItem is a line item on the shopping list.
type ShoppingItem struct {
	ID             int64   `json:"id"`
	InventoryID    *int64  `json:"inventory_id"`
	Name           string  `json:"name"`
	QuantityNeeded float64 `json:"quantity_needed"`
	Unit           string  `json:"unit"`
	Checked        bool    `json:"checked"`
	Source         string  `json:"source"` // manual | threshold | recipe | calendar
}

// RecipeIngredient is one ingredient line within a recipe.
type RecipeIngredient struct {
	ID          int64   `json:"id"`
	RecipeID    int64   `json:"recipe_id"`
	InventoryID *int64  `json:"inventory_id"`
	Name        string  `json:"name"`
	Quantity    float64 `json:"quantity"`
	Unit        string  `json:"unit"`
}

// Recipe is a named set of ingredients and instructions.
type Recipe struct {
	ID           int64              `json:"id"`
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	Instructions string             `json:"instructions"`
	Tags         string             `json:"tags"` // comma-separated
	Servings     int                `json:"servings"`
	Ingredients  []RecipeIngredient `json:"ingredients"`
}

// MealEntry is a single meal slot on the calendar.
type MealEntry struct {
	ID       int64  `json:"id"`
	Date     string `json:"date"`      // YYYY-MM-DD
	MealSlot string `json:"meal_slot"` // breakfast | lunch | dinner
	RecipeID int64  `json:"recipe_id"`
	Servings int    `json:"servings"`
}
