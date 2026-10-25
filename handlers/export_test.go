package handlers

import "time"

func SetMealsNow(f func() time.Time) (restore func()) {
	old := mealsNow
	mealsNow = f
	return func() { mealsNow = old }
}

var MealStatsBounds = mealStatsBounds
