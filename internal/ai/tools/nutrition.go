package tools

import (
	"math"
	"strings"
)

// Lightweight nutrition estimation used by the agent tools.
// Values are per 100 g. Mirrors the OG src/lib/ai/nutrition.ts table.

type macro struct {
	Kcal    float64
	Protein float64
	Carbs   float64
	Fat     float64
}

var per100g = map[string]macro{
	"chicken":        {165, 31, 0, 3.6},
	"chicken breast": {165, 31, 0, 3.6},
	"beef":           {250, 26, 0, 17},
	"pork":           {242, 27, 0, 14},
	"salmon":         {208, 20, 0, 13},
	"tuna":           {132, 28, 0, 1.3},
	"egg":            {155, 13, 1.1, 11},
	"eggs":           {155, 13, 1.1, 11},
	"tofu":           {76, 8, 1.9, 4.8},
	"tempeh":         {192, 20, 7.6, 11},
	"rice":           {130, 2.7, 28, 0.3},
	"brown rice":     {123, 2.7, 26, 1},
	"pasta":          {158, 5.8, 31, 0.9},
	"bread":          {265, 9, 49, 3.2},
	"oats":           {389, 17, 66, 7},
	"potato":         {77, 2, 17, 0.1},
	"spinach":        {23, 2.9, 3.6, 0.4},
	"broccoli":       {34, 2.8, 7, 0.4},
	"tomato":         {18, 0.9, 3.9, 0.2},
	"tomatoes":       {18, 0.9, 3.9, 0.2},
	"onion":          {40, 1.1, 9.3, 0.1},
	"carrot":         {41, 0.9, 10, 0.2},
	"banana":         {89, 1.1, 23, 0.3},
	"apple":          {52, 0.3, 14, 0.2},
	"avocado":        {160, 2, 9, 15},
	"olive oil":      {884, 0, 0, 100},
	"butter":         {717, 0.9, 0.1, 81},
	"cheese":         {402, 25, 1.3, 33},
	"cheddar":        {402, 25, 1.3, 33},
	"mozzarella":     {280, 28, 3.1, 17},
	"feta":           {264, 14, 4.1, 21},
	"yogurt":         {59, 10, 3.6, 0.4},
	"greek yogurt":   {59, 10, 3.6, 0.4},
	"milk":           {42, 3.4, 5, 1},
	"almond":         {579, 21, 22, 50},
	"almonds":        {579, 21, 22, 50},
	"black beans":    {132, 8.9, 24, 0.5},
	"chickpeas":      {164, 8.9, 27, 2.6},
	"lentils":        {116, 9, 20, 0.4},
	"sweet potato":   {86, 1.6, 20, 0.1},
	"kale":           {35, 2.9, 4.4, 1.5},
	"mushroom":       {22, 3.1, 3.3, 0.3},
	"mushrooms":      {22, 3.1, 3.3, 0.3},
	"shrimp":         {99, 24, 0.2, 0.3},
	"quinoa":         {120, 4.4, 21, 1.9},
	"flour":          {364, 10, 76, 1},
	"sugar":          {387, 0, 100, 0},
	"honey":          {304, 0.3, 82, 0},
}

var defaultPer100g = macro{Kcal: 200, Protein: 8, Carbs: 25, Fat: 7}

func lookupIngredient(name string) macro {
	key := strings.ToLower(strings.TrimSpace(name))
	if m, ok := per100g[key]; ok {
		return m
	}
	return defaultPer100g
}

// MacroTotals mirrors the OG nutrition output shape.
type MacroTotals struct {
	Calories     float64 `json:"calories"`
	ProteinGrams float64 `json:"proteinGrams"`
	CarbsGrams   float64 `json:"carbsGrams"`
	FatGrams     float64 `json:"fatGrams"`
}

type gramsInput struct {
	Name  string  `json:"name"`
	Grams float64 `json:"grams"`
}

// EstimateNutrition computes per-serving and total macros for a set of
// ingredients expressed in grams.
func EstimateNutrition(ingredients []gramsInput, servings int) map[string]any {
	total := MacroTotals{}
	for _, it := range ingredients {
		per := lookupIngredient(it.Name)
		f := it.Grams / 100
		total.Calories += per.Kcal * f
		total.ProteinGrams += per.Protein * f
		total.CarbsGrams += per.Carbs * f
		total.FatGrams += per.Fat * f
	}
	s := servings
	if s < 1 {
		s = 1
	}
	r1 := func(n float64) float64 { return math.Round(n*10) / 10 }
	per := MacroTotals{
		Calories:     math.Round(total.Calories / float64(s)),
		ProteinGrams: r1(total.ProteinGrams / float64(s)),
		CarbsGrams:   r1(total.CarbsGrams / float64(s)),
		FatGrams:     r1(total.FatGrams / float64(s)),
	}
	return map[string]any{
		"perServing": per,
		"total": MacroTotals{
			Calories:     math.Round(total.Calories),
			ProteinGrams: r1(total.ProteinGrams),
			CarbsGrams:   r1(total.CarbsGrams),
			FatGrams:     r1(total.FatGrams),
		},
	}
}

// IngredientToGrams converts an amount+unit into grams (mirrors OG).
func IngredientToGrams(amount float64, unit string) float64 {
	u := strings.ToLower(strings.TrimSpace(unit))
	switch u {
	case "g", "gram", "grams":
		return amount
	case "kg", "kilogram", "kilograms":
		return amount * 1000
	case "ml", "milliliter", "milliliters":
		return amount
	case "l", "liter", "liters":
		return amount * 1000
	case "cup", "cups":
		return amount * 240
	case "tbsp", "tablespoon", "tablespoons":
		return amount * 15
	case "tsp", "teaspoon", "teaspoons":
		return amount * 5
	case "oz", "ounce", "ounces":
		return amount * 28
	case "lb", "lbs", "pound", "pounds":
		return amount * 454
	case "piece", "pieces", "whole", "clove", "cloves":
		return amount * 50
	case "can", "cans":
		return amount * 400
	case "handful", "handfuls":
		return amount * 30
	default:
		return amount
	}
}

// KnownIngredients lists the per-100g table (for local search fallback).
func KnownIngredients() []map[string]any {
	out := make([]map[string]any, 0, len(per100g))
	for name, n := range per100g {
		out = append(out, map[string]any{
			"name":    name,
			"kcal":    n.Kcal,
			"protein": n.Protein,
			"carbs":   n.Carbs,
			"fat":     n.Fat,
		})
	}
	return out
}
