export function scanner() {
  return {
    scannerOpen: false,

    scannerMode: '',

    _codeReader: null,

    _stream: null,

    _scannerActive: false,

    _scanRaf: null,

    groceryScanOpen: false,

    groceryScanPaused: false,

    groceryScanReview: false,

    groceryPendingItems: [],

    _groceryProcessing: false,

    _scanCooldown: false,

    groceryNewItem: null, // prefill object when paused for unknown item

    groceryCameraError: '',

    removeStockScanOpen: false,

    _removeStockStream: null,

    _removeStockRaf: null,

    _removeStockActive: false,

    groceryItemForm: { name: '', quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0, barcode: '', quantity_per_scan: 1 },

    async openScanner(mode) {
      if (this._scannerActive) { this.closeScanner(); }
      this.scannerMode = mode;
      this.scannerOpen = true;
      await this.$nextTick();
      if (!this.scannerOpen) return;

      const video = document.getElementById('scanner-video');
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }
        });
        if (!this.scannerOpen) { stream.getTracks().forEach(t => t.stop()); return; }
        this._stream = stream;
        video.srcObject = stream;
        await video.play();
        await new Promise(resolve => {
          if (video.readyState >= 2) { resolve(); return; }
          video.addEventListener('canplay', resolve, { once: true });
        });
      } catch (err) {
        this.closeScanner();
        const msg = (err.name === 'NotAllowedError' || err.name === 'PermissionDeniedError')
          ? 'Camera access denied — enter barcode manually'
          : 'Camera unavailable — enter barcode manually';
        this.showToast(msg);
        return;
      }

      this._scannerActive = true;

      if (typeof BarcodeDetector !== 'undefined') {
        const detector = new BarcodeDetector({ formats: ['ean_13','ean_8','upc_a','upc_e','code_128','code_39','qr_code','itf','codabar'] });
        const scan = async () => {
          if (!this._scannerActive) return;
          try {
            const codes = await detector.detect(video);
            if (codes.length > 0) { this.onBarcodeScanned(codes[0].rawValue); return; }
          } catch {}
          this._scanRaf = requestAnimationFrame(scan);
        };
        this._scanRaf = requestAnimationFrame(scan);
      } else {
        this._codeReader = new ZXing.BrowserMultiFormatReader();
        this._codeReader.decodeFromVideoElementContinuously(video, (result, err) => {
          if (result) this.onBarcodeScanned(result.getText());
        });
      }
    },

    closeScanner() {
      this._scannerActive = false;
      if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
      if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
      if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
      this.scannerOpen = false;
      this.scannerMode = '';
    },

    async onBarcodeScanned(code) {
      if (!this.scannerOpen) return; // guard against duplicate ZXing callbacks
      const mode = this.scannerMode;
      this.scannedCode = code;
      this.closeScanner();
      if (mode === 'fill') {
        this.itemForm.barcode = code;
        return;
      }
      // mode === 'find': look up in our inventory first
      try {
        const res = await fetch(this.baseUrl + '/api/inventory/barcode/' + encodeURIComponent(code));
        if (res.ok) {
          const item = await res.json();
          if (item.id === 0) {
            // Known product with no stock left: add a new lot instead of editing one.
            this.openAddItem();
            Object.assign(this.itemForm, {
              name: item.name, unit: item.unit, preferred_unit: item.preferred_unit || '',
              low_threshold: item.low_threshold, barcode: code, quantity_per_scan: item.quantity_per_scan,
            });
            return;
          }
          this.editItem(item);
          return;
        }
      } catch {
        // fall through to Open Food Facts
      }
      // Not in inventory — try Open Food Facts
      let prefill = { barcode: code };
      let scannedName = '', scannedQty = null, scannedUnit = null;
      try {
        const off = await fetch('https://world.openfoodfacts.org/api/v2/product/' + encodeURIComponent(code) + '.json');
        if (off.ok) {
          const data = await off.json();
          if (data.status === 1 && data.product) {
            const p = data.product;
            if (p.product_name) scannedName = p.product_name;
            if (p.product_name) prefill.name = p.product_name;

            const parsed = this.parseOffQuantity(p);
            if (parsed) {
              scannedQty = parsed.qty;
              scannedUnit = parsed.unit;
              prefill.quantity = parsed.qty;
              prefill.unit = parsed.unit;
              prefill.preferred_unit = parsed.unit;
            }
          }
        }
      } catch {
        // silent fallback — open form with just barcode
      }

      // Try to find merge candidates via similar search if we have a name
      if (scannedName && scannedName.trim()) {
        try {
          const similarRes = await fetch(this.baseUrl + '/api/inventory/similar?name=' + encodeURIComponent(scannedName));
          if (similarRes.ok) {
            const candidates = await similarRes.json();
            if (candidates && candidates.length > 0) {
              // Show merge dialog
              this.skuMergeDialog = {
                barcode: code,
                name: scannedName,
                quantity: scannedQty || 1,
                unit: scannedUnit || '',
                candidates: candidates,
                mode: 'find',
              };
              return;
            }
          }
        } catch {}
      }
      this.openAddItem();
      Object.assign(this.itemForm, prefill);
    },

    parseOffQuantity(product) {
      const offUnitMap = {
        'g': 'g', 'gram': 'g', 'grams': 'g',
        'kg': 'kg', 'kilogram': 'kg', 'kilograms': 'kg',
        'oz': 'oz', 'ounce': 'oz', 'ounces': 'oz',
        'lb': 'lb', 'lbs': 'lb', 'pound': 'lb', 'pounds': 'lb',
        'ml': 'ml', 'milliliter': 'ml', 'milliliters': 'ml', 'millilitre': 'ml',
        'l': 'L', 'L': 'L', 'liter': 'L', 'liters': 'L', 'litre': 'L', 'litres': 'L',
        'cl': 'ml',
      };
      let qty = null, unit = null;
      if (product.product_quantity && product.product_quantity_unit) {
        const rawUnit = product.product_quantity_unit.trim().toLowerCase();
        const mapped = offUnitMap[rawUnit] || offUnitMap[product.product_quantity_unit.trim()];
        if (mapped) {
          qty = parseFloat(product.product_quantity);
          unit = mapped;
          if (rawUnit === 'cl') qty = qty * 10;
        }
      }
      if ((qty === null || unit === null) && product.quantity) {
        const m = product.quantity.trim().match(/^([\d.,]+)\s*([a-zA-Z]+)/);
        if (m) {
          const rawUnit = m[2].trim().toLowerCase();
          const mapped = offUnitMap[rawUnit] || offUnitMap[m[2].trim()];
          if (mapped) {
            qty = parseFloat(m[1].replace(',', '.'));
            unit = mapped;
            if (rawUnit === 'cl') qty = qty * 10;
          }
        }
      }
      return (qty !== null && unit !== null && !isNaN(qty)) ? { qty, unit } : null;
    },

    async openGroceryScanner() {
      if (this._scannerActive) this.closeScanner();
      this.groceryScanOpen = true;
      this.groceryScanPaused = false;
      this.groceryScanReview = false;
      this.groceryPendingItems = [];
      this._groceryProcessing = false;
      this._scanCooldown = false;
      this.groceryNewItem = null;
      this.groceryCameraError = '';
      await this.$nextTick();
      await this._startGroceryCamera();
    },

    async _startGroceryCamera() {
      if (!this.groceryScanOpen) return;
      const video = document.getElementById('grocery-scanner-video');
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }
        });
        if (!this.groceryScanOpen) { stream.getTracks().forEach(t => t.stop()); return; }
        this._stream = stream;
        video.srcObject = stream;
        await new Promise(resolve => { video.onloadedmetadata = resolve; });
        video.play();
      } catch {
        this.groceryCameraError = 'Camera access denied. Please allow camera access and try again.';
        return;
      }

      this._scannerActive = true;
      let lastScanErrorToast = 0;

      if (typeof BarcodeDetector !== 'undefined') {
        const detector = new BarcodeDetector({ formats: ['ean_13','ean_8','upc_a','upc_e','code_128','code_39','qr_code','itf','codabar'] });
        const scan = async () => {
          if (!this._scannerActive || this.groceryScanPaused) {
            if (this._scannerActive) this._scanRaf = requestAnimationFrame(scan);
            return;
          }
          try {
            const codes = await detector.detect(video);
            if (codes.length > 0) { lastScanErrorToast = 0; this.onGroceryBarcodeScanned(codes[0].rawValue); }
          } catch {
            const now = Date.now();
            if (now - lastScanErrorToast > 2000) {
              lastScanErrorToast = now;
              this.showToast("Couldn't read barcode", 'error');
            }
          }
          this._scanRaf = requestAnimationFrame(scan);
        };
        this._scanRaf = requestAnimationFrame(scan);
      } else {
        this._codeReader = new ZXing.BrowserMultiFormatReader();
        this._codeReader.decodeFromVideoElementContinuously(video, (result, err) => {
          if (result && !this.groceryScanPaused) { lastScanErrorToast = 0; this.onGroceryBarcodeScanned(result.getText()); return; }
          if (err && !(err instanceof ZXing.NotFoundException)) {
            const now = Date.now();
            if (now - lastScanErrorToast > 2000) {
              lastScanErrorToast = now;
              this.showToast("Couldn't read barcode", 'error');
            }
          }
        });
      }
    },

    closeGroceryScanner() {
      this.groceryScanOpen = false;
      this.groceryScanPaused = false;
      this.groceryScanReview = false;
      this.groceryPendingItems = [];
      this._scanCooldown = false;
      this.groceryNewItem = null;
      this._scannerActive = false;
      if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
      if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
      if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
    },

    finishGroceryScan() {
      if (this.groceryPendingItems.length === 0) {
        this.closeGroceryScanner();
        return;
      }
      this._scannerActive = false;
      if (this._scanRaf) { cancelAnimationFrame(this._scanRaf); this._scanRaf = null; }
      if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
      if (this._stream) { this._stream.getTracks().forEach(t => t.stop()); this._stream = null; }
      this.groceryScanPaused = false;
      this.groceryNewItem = null;
      this.groceryScanReview = true;
    },

    async commitGroceryItems() {
      let successCount = 0, failed = 0;
      for (const pending of this.groceryPendingItems) {
        try {
          const cur = await fetch(this.baseUrl + '/api/inventory/barcode/' + encodeURIComponent(pending.barcode));
          if (!cur.ok) { failed++; continue; }
          const item = await cur.json();
          const newQty = parseFloat((item.quantity + pending.qty).toFixed(4));
          // A product with no lots left (id 0) gets a new lot; otherwise top up the lot.
          const patch = item.id === 0
            ? await this.apiFetch('/api/inventory/', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: item.name, quantity: pending.qty, unit: item.unit, preferred_unit: item.preferred_unit || '', barcode: pending.barcode }),
              }, { onError: () => {} })
            : await this.apiFetch('/api/inventory/' + item.id, {
                method: 'PATCH',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ quantity: newQty }),
              }, { onError: () => {} });
          if (patch) successCount++; else failed++;
        } catch { failed++; }
      }
      this.fetchInventory();
      this.closeGroceryScanner();
      if (failed > 0) this.showToast(`Updated ${successCount} item(s), ${failed} failed`, 'error');
      else this.showToast(`Updated ${successCount} item(s)`);
    },

    async onGroceryBarcodeScanned(code) {
      if (!this.groceryScanOpen || this.groceryScanPaused || this.groceryScanReview || this._groceryProcessing || this._scanCooldown) return;
      this.scannedCode = code;
      this._groceryProcessing = true;
      this._scanCooldown = true;
      setTimeout(() => { this._scanCooldown = false; }, 2500);

      try {
        const res = await fetch(this.baseUrl + '/api/inventory/barcode/' + encodeURIComponent(code));
        if (res.ok) {
          const item = await res.json();
          // Add to or update pending list
          const scanQty = item.quantity_per_scan > 0 ? item.quantity_per_scan : 1;
          const existing = this.groceryPendingItems.find(p => p.barcode === code);
          if (existing) {
            existing.qty = parseFloat((existing.qty + scanQty).toFixed(4));
          } else {
            this.groceryPendingItems.push({ id: item.id, name: item.name, qty: scanQty, unit: item.unit, barcode: code });
          }
          const entry = this.groceryPendingItems.find(p => p.barcode === code);
          this.showToast(`${item.name} +${scanQty} ${item.unit}`, 'success', `${entry.qty} ${item.unit}`);
          this._groceryProcessing = false;

          // OFf refinement in background (non-blocking)
          const qtyAtScanTime = entry.qty;
          (async () => {
            try {
              const offPromise = fetch('https://world.openfoodfacts.org/api/v2/product/' + encodeURIComponent(code) + '.json').then(r => r.json());
              const offData = await Promise.race([offPromise, new Promise((_, r) => setTimeout(() => r(new Error('timeout')), 3000))]);
              if (offData && offData.status === 1 && offData.product) {
                const parsed = this.parseOffQuantity(offData.product);
                if (parsed) {
                  const targetUnit = item.preferred_unit || item.unit;
                  const converted = this.convertUnits(parsed.qty, parsed.unit, targetUnit);
                  if (converted !== null && converted !== 1) {
                    const pendingEntry = this.groceryPendingItems.find(p => p.barcode === code);
                    if (pendingEntry && pendingEntry.qty === qtyAtScanTime) {
                      pendingEntry.qty = parseFloat(converted.toFixed(4));
                      pendingEntry.unit = targetUnit;
                    }
                  }
                }
              }
            } catch { /* silent */ }
          })();
          return;
        }
      } catch { /* fall through to new item flow */ }

      // Not in inventory — pause for unknown item form
      this._groceryProcessing = false;
      this.groceryScanPaused = true;
      let prefill = { barcode: code, quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0 };

      try {
        const offPromise = fetch(`https://world.openfoodfacts.org/api/v2/product/${code}.json`).then(r => r.json());
        const offTimeout = new Promise((_, reject) => setTimeout(() => reject(new Error('timeout')), 3000));
        let offData = null;
        try { offData = await Promise.race([offPromise, offTimeout]); } catch { /* silent fallback */ }
        if (offData && offData.status === 1 && offData.product) {
          const p = offData.product;
          if (p.product_name) prefill.name = p.product_name;
          const parsed = this.parseOffQuantity(p);
          if (parsed) {
            prefill.quantity = parsed.qty;
            prefill.unit = parsed.unit;
            prefill.preferred_unit = parsed.unit;
          }
        }
      } catch { /* silent */ }

      // Location pre-fill from existing inventory
      if (prefill.name) {
        try {
          const invRes = await fetch(this.baseUrl + '/api/inventory/?name=' + encodeURIComponent(prefill.name));
          if (invRes.ok) {
            const invData = await invRes.json();
            if (invData && invData.length > 0 && invData[0].location) prefill.location = invData[0].location;
          }
        } catch { /* silent */ }
      }

      // Try to find merge candidates via similar search if we have a name
      if (prefill.name && prefill.name.trim()) {
        try {
          const similarRes = await fetch(this.baseUrl + '/api/inventory/similar?name=' + encodeURIComponent(prefill.name));
          if (similarRes.ok) {
            const candidates = await similarRes.json();
            if (candidates && candidates.length > 0) {
              // Show merge dialog
              this.skuMergeDialog = {
                barcode: code,
                name: prefill.name,
                quantity: prefill.quantity || 1,
                unit: prefill.unit || '',
                candidates: candidates,
                mode: 'grocery',
              };
              this.groceryScanPaused = false;
              return;
            }
          }
        } catch {}
      }

      this.groceryNewItem = prefill;
      this.groceryItemForm = { name: prefill.name || '', quantity: prefill.quantity, unit: prefill.unit, preferred_unit: prefill.preferred_unit, location: prefill.location, expiration_date: '', low_threshold: 0, barcode: code, quantity_per_scan: prefill.quantity || 1 };
    },

    async confirmGroceryNewItem() {
      const res = await this.apiFetch('/api/inventory/', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(this.groceryItemForm),
      }, { errorPrefix: 'Failed to add item' });
      if (res) {
        this.showToast(`Added ${this.groceryItemForm.name || 'item'} to inventory`);
        this.fetchInventory(); // background refresh, no await
      }
      this.skipGroceryNewItem();
    },

    skipGroceryNewItem() {
      this.groceryNewItem = null;
      this.groceryScanPaused = false;
      this.groceryItemForm = { name: '', quantity: 1, unit: '', preferred_unit: '', location: '', expiration_date: '', low_threshold: 0, barcode: '' };
    },

    // --- Remove Stock Scanner ---
    async openRemoveStockScanner() {
      this.removeStockScanOpen = true;
      this._removeStockActive = false;
      await this.$nextTick();
      const video = document.getElementById('remove-stock-video');
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          video: { facingMode: { ideal: 'environment' }, width: { ideal: 1920 }, height: { ideal: 1080 } }
        });
        if (!this.removeStockScanOpen) { stream.getTracks().forEach(t => t.stop()); return; }
        this._removeStockStream = stream;
        video.srcObject = stream;
        await video.play();
      } catch {
        this.removeStockScanOpen = false;
        this.showToast('Camera unavailable', 'error');
        return;
      }
      this._removeStockActive = true;
      let cooldown = false;
      if (typeof BarcodeDetector !== 'undefined') {
        const detector = new BarcodeDetector({ formats: ['ean_13','ean_8','upc_a','upc_e','code_128','code_39'] });
        const scan = async () => {
          if (!this._removeStockActive) return;
          if (!cooldown) {
            try {
              const codes = await detector.detect(video);
              if (codes.length > 0) {
                cooldown = true;
                await this.onRemoveStockScanned(codes[0].rawValue);
                setTimeout(() => { cooldown = false; }, 2000);
              }
            } catch {}
          }
          this._removeStockRaf = requestAnimationFrame(scan);
        };
        this._removeStockRaf = requestAnimationFrame(scan);
      } else {
        this._codeReader = new ZXing.BrowserMultiFormatReader();
        this._codeReader.decodeFromVideoElementContinuously(video, async (result) => {
          if (result && !cooldown) {
            cooldown = true;
            await this.onRemoveStockScanned(result.getText());
            setTimeout(() => { cooldown = false; }, 2000);
          }
        });
      }
    },

    closeRemoveStockScanner() {
      this._removeStockActive = false;
      if (this._removeStockRaf) { cancelAnimationFrame(this._removeStockRaf); this._removeStockRaf = null; }
      if (this._codeReader) { this._codeReader.reset(); this._codeReader = null; }
      if (this._removeStockStream) { this._removeStockStream.getTracks().forEach(t => t.stop()); this._removeStockStream = null; }
      this.removeStockScanOpen = false;
    },

    async onRemoveStockScanned(code) {
      try {
        const res = await fetch(this.baseUrl + '/api/inventory/barcode/' + encodeURIComponent(code));
        if (!res.ok) { this.showToast('Item not found', 'error'); return; }
        const item = await res.json();
        if (item.id === 0) { this.showToast(`${item.name}: nothing in stock`, 'error'); return; }
        const scanQty = item.quantity_per_scan > 0 ? item.quantity_per_scan : 1;
        const patch = await this.apiFetch('/api/inventory/' + item.id + '/deduct?source=barcode_remove', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ quantity: scanQty }),
        }, { errorPrefix: 'Update failed' });
        if (patch) {
          this.showToast(`${item.name}: −${scanQty} ${item.preferred_unit || item.unit}`, 'success');
          this.fetchInventory();
        }
      } catch {
        this.showToast('Scan error', 'error');
      }
    },
  };
}
