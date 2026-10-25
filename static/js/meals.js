export function meals() {
  return {
    // History
    historyItems: [],

    historyStatsData: null,

    // Meals
    meals: [],

    mealsTotal: 0,

    mealsOffset: 0,

    mealsStats: null,

    mealStatsLoaded: false,

    // --- History ---
    async fetchHistory() {
      const res = await fetch(this.baseUrl + '/api/history?limit=50');
      if (res.ok) {
        const data = await res.json();
        this.historyItems = data.rows || [];
      }
    },

    // --- Meals ---
    async fetchMeals() {
      this.mealsOffset = 0;
      const res = await fetch(this.baseUrl + '/api/meals?limit=20&offset=0');
      if (res.ok) {
        const data = await res.json();
        this.meals = data.meals;
        this.mealsTotal = data.total;
      }
    },

    async loadMoreMeals() {
      this.mealsOffset += 20;
      const res = await fetch(this.baseUrl + '/api/meals?limit=20&offset=' + this.mealsOffset);
      if (res.ok) {
        const data = await res.json();
        this.meals = this.meals.concat(data.meals);
        this.mealsTotal = data.total;
      }
    },

    async fetchMealStats() {
      const tzOffset = -new Date().getTimezoneOffset();
      const res = await fetch(this.baseUrl + '/api/meals/stats?tz_offset=' + tzOffset + '&tz=' + encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone || ''));
      if (res.ok) this.mealsStats = await res.json();
    },
  };
}
