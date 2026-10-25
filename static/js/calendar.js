export function calendar() {
  return {
    // Calendar
    calendarEntries: [],

    calendarWeekStart: null,

    mealSlots: ['breakfast', 'lunch', 'dinner', 'snack'],

    calendarEntryForm: { id: null, date: '', meal_slot: 'dinner', recipe_id: '', servings: null },

    // --- Calendar ---
    getMonday(d) {
      const day = d.getDay();
      const diff = d.getDate() - day + (day === 0 ? -6 : 1);
      const monday = new Date(d.setDate(diff));
      monday.setHours(0, 0, 0, 0);
      return monday;
    },

    get calendarWeekLabel() {
      if (!this.calendarWeekStart) return '';
      const end = new Date(this.calendarWeekStart);
      end.setDate(end.getDate() + 6);
      return this.calendarWeekStart.toLocaleDateString('en-US', { month: 'short', day: 'numeric' }) +
        ' – ' + end.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
    },

    get calendarDays() {
      if (!this.calendarWeekStart) return [];
      const days = [];
      for (let i = 0; i < 7; i++) {
        const d = new Date(this.calendarWeekStart);
        d.setDate(d.getDate() + i);
        days.push({
          date: d.toISOString().split('T')[0],
          label: d.toLocaleDateString('en-US', { weekday: 'long', month: 'short', day: 'numeric' }),
        });
      }
      return days;
    },

    prevWeek() {
      const d = new Date(this.calendarWeekStart);
      d.setDate(d.getDate() - 7);
      this.calendarWeekStart = d;
      this.fetchCalendar();
    },

    nextWeek() {
      const d = new Date(this.calendarWeekStart);
      d.setDate(d.getDate() + 7);
      this.calendarWeekStart = d;
      this.fetchCalendar();
    },

    async fetchCalendar() {
      const start = this.calendarWeekStart.toISOString().split('T')[0];
      const res = await fetch(this.baseUrl + '/api/calendar/week?start=' + start);
      this.calendarEntries = await res.json();
    },

    getCalendarEntries(date, slot) {
      return this.calendarEntries.filter(e => e.date === date && e.meal_slot === slot);
    },

    getOtherCalendarEntries(date) {
      return this.calendarEntries.filter(e => e.date === date && !this.mealSlots.includes(e.meal_slot));
    },

    getRecipeName(recipeId) {
      const r = this.recipes.find(r => r.id === recipeId);
      return r ? r.name : 'Unknown';
    },

    openAddCalendarEntry(date, slot) {
      this.calendarEntryForm = { id: null, date, meal_slot: slot, recipe_id: '', servings: null };
      this.modal = 'calendarEntry';
    },

    openEditCalendarEntry(entry) {
      this.calendarEntryForm = {
        id: entry.id, date: entry.date,
        meal_slot: this.mealSlots.includes(entry.meal_slot) ? entry.meal_slot : 'dinner',
        recipe_id: entry.recipe_id, servings: entry.servings,
      };
      this.modal = 'calendarEntry';
    },

    async saveCalendarEntry() {
      const f = this.calendarEntryForm;
      let res;
      if (f.id) {
        if (!f.date || !(f.servings > 0)) { this.showToast('Date and servings are required', 'error'); return; }
        res = await this.apiFetch('/api/calendar/' + f.id, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ date: f.date, meal_slot: f.meal_slot, servings: f.servings }),
        });
      } else {
        if (!f.recipe_id) return;
        if (!(f.servings > 0)) this.onCalendarRecipeChange();
        res = await this.apiFetch('/api/calendar/', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(f),
        });
      }
      if (!res) return;
      this.modal = null;
      await this.fetchCalendar();
      this.showToast(f.id ? 'Meal updated' : 'Meal added');
    },

    async deleteCalendarEntry(id) {
      const res = await this.apiFetch('/api/calendar/' + id, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Remove failed' });
      if (!res) return;
      await this.fetchCalendar();
      this.showToast('Meal removed');
    },

    async generateWeeklyShopping() {
      const start = this.calendarWeekStart.toISOString().split('T')[0];
      const res = await this.apiFetch('/api/calendar/generate-weekly-shopping?start=' + start, { method: 'POST', headers: { 'Content-Type': 'application/json' } });
      if (!res) return;
      const data = await res.json();
      this.showToast(`Added ${data.added ?? data.items.length} new, updated ${data.merged ?? 0} item(s)`);
      await this.fetchShoppingList();
      this.activeTab = 'shopping';
    },
  };
}
