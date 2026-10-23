    // Canary private finite-intermediate profile. No public binding or user marker.
    const canaryProcedures = new WeakSet();
    const canaryDescriptor = Object.getOwnPropertyDescriptor;
    const canaryKeys = Object.keys;
    const canaryArray = Array.isArray;
    const canaryIsFinite = Number.isFinite;
    function canaryReject() {
        throw new Error('JSON query result rejected by finite profile');
    }
    function canaryFinite(value) {
        let remaining = 65536;
        function visit(current, depth) {
            if (depth > 128 || remaining === 0) canaryReject();
            remaining--;
            if (typeof current === 'number') {
                if (!canaryIsFinite(current)) canaryReject();
                return;
            }
            if (current === null || typeof current !== 'object' || canaryProcedures.has(current)) return;
            if (canaryArray(current)) {
                const length = canaryDescriptor(current, 'length').value;
                if (length > remaining) canaryReject();
                for (let i = 0; i < length; i++) {
                    const descriptor = canaryDescriptor(current, i);
                    if (descriptor && !Object.prototype.hasOwnProperty.call(descriptor, 'value')) canaryReject();
                    visit(descriptor ? descriptor.value : undefined, depth + 1);
                }
            } else {
                const keys = canaryKeys(current);
                if (keys.length > remaining) canaryReject();
                for (let i = 0; i < keys.length; i++) {
                    const descriptor = canaryDescriptor(current, keys[i]);
                    if (!Object.prototype.hasOwnProperty.call(descriptor, 'value')) canaryReject();
                    visit(descriptor.value, depth + 1);
                }
            }
        }
        visit(value, 0);
        return value;
    }
