export function recipes() {
  return {
    // Recipes
    recipes: [],

    recipeTagFilter: '',

    recipeShowArchived: false,

    recipeAvailableOnly: false,

    selectedRecipe: null,

    settings: {},

    recipeServings: {},

    recipeForm: { id: null, name: '', description: '', instructions: '', tags: '', servings: 2, ingredients: [] },

    recipeViewDate: '',

    recipeViewSlot: 'dinner',

    recipeImportTab: 'url',

    recipeImportUrl: '',

    recipeImportFile: null,

    recipeImportText: '',

    recipeImporting: false,

    recipeImportError: '',

    cookItServings: 2,

    cookItConfirm: null,

    // --- Recipes ---
    async fetchRecipes() {
      const params = new URLSearchParams();
      if (this.recipeTagFilter) params.set('tag', this.recipeTagFilter);
      if (this.recipeAvailableOnly) params.set('available_only', 'true');
      params.set('include_archived', 'true'); // calendar needs archived names; list filters client-side
      const res = await fetch(this.baseUrl + '/api/recipes/?' + params);
      this.recipes = await res.json();
    },

    openAddRecipe() {
      this.recipeForm = { id: null, name: '', description: '', instructions: '', tags: '', servings: this.defaultServings(), ingredients: [] };
      this.modal = 'recipe';
    },

    editRecipe(recipe) {
      this.recipeForm = {
        id: recipe.id,
        name: recipe.name,
        description: recipe.description || '',
        instructions: recipe.instructions || '',
        tags: recipe.tags || '',
        servings: recipe.servings || this.defaultServings(),
        ingredients: (recipe.ingredients || []).map(i => ({
          product_id: i.product_id || null,
          name: i.name,
          quantity: i.quantity,
          unit: i.unit || '',
        })),
      };
      this.modal = 'recipe';
    },

    openImportRecipe() {
      this.recipeImportTab = 'url';
      this.recipeImportUrl = '';
      this.recipeImportFile = null;
      this.recipeImportText = '';
      this.recipeImporting = false;
      this.recipeImportError = '';
      this.modal = 'recipeImport';
    },

    async runRecipeImport() {
      this.recipeImportError = '';
      if (this.recipeImportTab === 'paste') {
        const text = this.recipeImportText.trim();
        if (!text) return;
        this.recipeForm = { id: null, name: '', description: '', instructions: text, tags: '', servings: this.defaultServings(), ingredients: [] };
        this.modal = 'recipe';
        return;
      }
      if (this.recipeImportTab === 'url') {
        if (!this.recipeImportUrl) return;
        this.recipeImporting = true;
        try {
          const res = await this.apiFetch('/api/recipes/import/url', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ url: this.recipeImportUrl }),
          }, { onError: m => { this.recipeImportError = m + ' — try the Paste tab instead.'; } });
          if (!res) return;
          const data = await res.json();
          this._applyImportData(data);
        } catch (e) {
          this.recipeImportError = 'Network error — try the Paste tab instead.';
        } finally {
          this.recipeImporting = false;
        }
        return;
      }
      if (this.recipeImportTab === 'pdf') {
        if (!this.recipeImportFile) return;
        this.recipeImporting = true;
        try {
          const fd = new FormData();
          fd.append('file', this.recipeImportFile);
          const res = await this.apiFetch('/api/recipes/import/pdf', { method: 'POST', body: fd }, { onError: m => { this.recipeImportError = m; } });
          if (!res) return;
          const data = await res.json();
          this._applyImportData(data);
        } catch (e) {
          this.recipeImportError = 'Network error';
        } finally {
          this.recipeImporting = false;
        }
      }
    },

    _applyImportData(data) {
      if (data.recipe) {
        const rec = data.recipe;
        const ingredients = (rec.ingredients || []).map(i => ({ name: i.name, quantity: i.quantity, unit: i.unit, product_id: null }));
        this.recipeForm = {
          id: null,
          name: rec.name || '',
          description: rec.description || '',
          instructions: rec.instructions || '',
          tags: rec.tags || '',
          servings: rec.servings || this.defaultServings(),
          ingredients: [],
        };
        this.modal = 'recipe';
        this.$nextTick(() => { this.recipeForm.ingredients = ingredients; });
      } else {
        this.recipeForm = { id: null, name: '', description: '', instructions: data.text || '', tags: '', servings: this.defaultServings(), ingredients: [] };
        this.modal = 'recipe';
      }
    },

    async saveRecipe() {
      if (!this.recipeForm.name) return;
      const isEdit = !!this.recipeForm.id;
      const res = await this.apiFetch(isEdit ? '/api/recipes/' + this.recipeForm.id : '/api/recipes/', {
        method: isEdit ? 'PUT' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(this.recipeForm),
      });
      if (!res) return;
      this.modal = null;
      await this.fetchRecipes();
      this.showToast(isEdit ? 'Recipe updated' : 'Recipe saved');
    },

    viewRecipe(recipe) {
      this.selectedRecipe = recipe;
      this.recipeViewDate = new Date().toISOString().split('T')[0];
      this.recipeViewSlot = 'dinner';
      this.modal = 'viewRecipe';
    },

    async fetchSettings() {
      try {
        const res = await fetch(this.baseUrl + '/api/settings');
        if (res.ok) this.settings = await res.json();
      } catch (e) { /* non-fatal */ }
    },

    defaultServings() {
      const n = parseInt(this.settings && this.settings.default_servings);
      return n > 0 ? n : 2;
    },

    // Denominator for scaling; mirrors units.ServingsScale (0 -> 1).
    recipeBaseServings(recipe) {
      return recipe && recipe.servings > 0 ? recipe.servings : 1;
    },

    recipeScaleServings(recipe) {
      const v = this.recipeServings[recipe.id];
      if (v > 0) return v;
      return recipe.servings > 0 ? recipe.servings : this.defaultServings();
    },

    setRecipeServings(recipe, v) {
      v = Math.max(1, parseInt(v) || 1);
      this.recipeServings = { ...this.recipeServings, [recipe.id]: v };
    },

    scaledQty(qty, servings, recipe) {
      return qty * servings / this.recipeBaseServings(recipe);
    },

    formatQty(n) {
      return parseFloat(Number(n).toFixed(2)).toString();
    },

    onCalendarRecipeChange() {
      const r = this.recipes.find(r => r.id === this.calendarEntryForm.recipe_id);
      this.calendarEntryForm.servings = r ? this.recipeScaleServings(r) : null;
    },

    async addRecipeToCalendar(recipe, servings) {
      if (!this.recipeViewDate) return;
      const res = await this.apiFetch('/api/calendar/', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ date: this.recipeViewDate, meal_slot: this.recipeViewSlot, recipe_id: recipe.id, servings }),
      });
      if (!res) return;
      this.modal = null;
      await this.fetchCalendar();
      this.showToast(`Added to calendar: ${this.recipeViewDate}`);
    },

    async addRecipeToShoppingList(recipe, servings) {
      const s = servings || this.recipeScaleServings(recipe);
      const res = await this.apiFetch('/api/recipes/' + recipe.id + '/add-to-shopping-list?servings=' + s, { method: 'POST', headers: { 'Content-Type': 'application/json' } });
      if (!res) return;
      const data = await res.json();
      this.modal = null;
      this.showToast(`Added ${data.added} new, updated ${data.merged || 0} item(s) on shopping list`);
      await this.fetchShoppingList();
    },

    cookIt(recipe, servings, calendarEntryId = null) {
      const s = servings || this.recipeScaleServings(recipe);
      this.cookItConfirm = { recipe, servings: s, calendarEntryId };
      this.modal = 'cookItConfirm';
    },

    async confirmCookIt() {
      const { recipe, servings, calendarEntryId } = this.cookItConfirm;
      const payload = { recipe_id: recipe.id, servings_made: servings };
      if (calendarEntryId) payload.calendar_entry_id = calendarEntryId;
      const res = await this.apiFetch('/api/meals', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      }, { errorPrefix: 'Failed to log meal' });
      if (!res) return;
      this.modal = null;
      this.cookItConfirm = null;
      this.showToast(`Cooked ${recipe.name} (${servings} serving${servings !== 1 ? 's' : ''})`, 'success');
      try { const b = await res.json(); (b.warnings || []).forEach(w => this.showToast(w, 'error')); } catch (e) {}
      await this.fetchInventory();
      await this.fetchExpiring();
      if (calendarEntryId) await this.fetchCalendar();
    },

    async setRecipeArchived(id, archive) {
      const res = await this.apiFetch('/api/recipes/' + id + (archive ? '/archive' : '/unarchive'), { method: 'POST', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: `Failed to ${archive ? 'archive' : 'restore'}` });
      if (!res) return;
      await this.fetchRecipes();
      this.showToast(archive ? 'Recipe archived' : 'Recipe restored');
    },

    archiveRecipe(id) { return this.setRecipeArchived(id, true); },

    unarchiveRecipe(id) { return this.setRecipeArchived(id, false); },

    async deleteRecipe(id) {
      let msg = 'Permanently delete this recipe? Cooking history will be kept. (Tip: Archive hides it without deleting.)';
      try {
        const res = await fetch(this.baseUrl + '/api/recipes/' + id + '/future-calendar');
        if (res.ok) {
          const { count } = await res.json();
          if (count > 0) msg = `This recipe is scheduled ${count} time${count !== 1 ? 's' : ''} on the calendar; those entries will be removed. Permanently delete anyway? Cooking history will be kept.`;
        }
      } catch (e) { /* fall through to plain confirm */ }
      if (!confirm(msg)) return;
      const delRes = await this.apiFetch('/api/recipes/' + id, { method: 'DELETE', headers: { 'Content-Type': 'application/json' } }, { errorPrefix: 'Failed to delete' });
      if (!delRes) return;
      await this.fetchRecipes();
      this.showToast('Recipe deleted');
    },

    get visibleRecipes() {
      return this.recipes.filter(r => this.recipeShowArchived || !r.archived);
    },

    get activeRecipes() {
      return this.recipes.filter(r => !r.archived);
    },
  };
}
