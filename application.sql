CREATE EVENT inventory_event (
    kind TEXT NOT NULL,
    id TEXT NOT NULL,
    sku TEXT NOT NULL,
    qty INTEGER NOT NULL
);

CREATE TABLE stock (
    sku TEXT NOT NULL,
    available INTEGER NOT NULL CHECK (available >= 0),
    PRIMARY KEY (sku)
);

CREATE TABLE reservations (
    id TEXT NOT NULL,
    sku TEXT NOT NULL,
    qty INTEGER NOT NULL CHECK (qty > 0),
    PRIMARY KEY (id)
);

CREATE NORMALIZER normalize ON gitseq_record
USING 'folds/normalize.jsonata'
EMITS inventory_event;

CREATE FOLD apply_inventory ON inventory_event
READ stock_row OPTIONAL ONE AS
    SELECT sku, available FROM stock WHERE sku = :event.sku
USING 'folds/inventory.jsonata'
WRITES stock, reservations;

CREATE EXPORT stock_rows AS SELECT sku, available FROM stock;
CREATE EXPORT reservation_rows AS SELECT id, sku, qty FROM reservations;
