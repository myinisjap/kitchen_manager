// Rooms and storage units: one shared hierarchy drives every location picker,
// fetched once rather than per-picker. See docs/rooms-storage-units-intent.md.
export function locations() {
  return {
    rooms: [],
    // The room select is view state only; the saved value is itemForm.storage_unit_id.
    pickerRoomId: '',
    newUnitName: '',

    async fetchRooms() {
      try {
        const res = await fetch(this.baseUrl + '/api/rooms');
        if (res.ok) this.rooms = await res.json();
      } catch { /* keep whatever we had */ }
    },

    // "Kitchen › Fridge", or "Unfiled" when the lot has no storage unit.
    // Accepts anything carrying a nested storage_unit (item, lot, candidate).
    locationLabel(obj) {
      const su = obj && obj.storage_unit;
      return su ? su.room + ' › ' + su.name : 'Unfiled';
    },

    unitsOfRoom(roomId) {
      const r = this.rooms.find(x => String(x.id) === String(roomId));
      return r ? r.storage_units : [];
    },

    // The room owning a unit, so editing an item preselects its room.
    roomIdForUnit(unitId) {
      if (!unitId) return '';
      const r = this.rooms.find(x => x.storage_units.some(u => u.id === unitId));
      return r ? r.id : '';
    },

    // Called when the picker opens: derive the room from the item's unit.
    syncPickerRoom() {
      this.pickerRoomId = this.roomIdForUnit(this.itemForm.storage_unit_id) || '';
      this.newUnitName = '';
    },

    // Changing room invalidates a unit from the previous room.
    onPickerRoomChange() {
      const units = this.unitsOfRoom(this.pickerRoomId);
      if (!units.some(u => u.id === this.itemForm.storage_unit_id)) {
        this.itemForm.storage_unit_id = null;
      }
      this.newUnitName = '';
    },

    // Alpine selects yield strings; the API wants a number or null.
    onPickerUnitChange(v) {
      this.itemForm.storage_unit_id = v === '' ? null : Number(v);
    },

    // Inline create, so filing an item doesn't need a settings detour.
    async addStorageUnitInline() {
      const name = (this.newUnitName || '').trim();
      if (!this.pickerRoomId || !name) return;
      const res = await this.apiFetch('/api/rooms/' + this.pickerRoomId + '/storage-units', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name }),
      }, { errorPrefix: 'Add storage unit failed' });
      if (!res) return;
      const unit = await res.json();
      await this.fetchRooms();
      this.itemForm.storage_unit_id = unit.id;
      this.newUnitName = '';
    },

    async addRoomInline(name) {
      const trimmed = (name || '').trim();
      if (!trimmed) return;
      const res = await this.apiFetch('/api/rooms', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: trimmed }),
      }, { errorPrefix: 'Add room failed' });
      if (!res) return;
      const room = await res.json();
      await this.fetchRooms();
      this.pickerRoomId = room.id;
    },
  };
}
