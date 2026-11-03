-- +goose Up
CREATE TABLE shop.products (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sku         TEXT        NOT NULL UNIQUE,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    price_cents BIGINT      NOT NULL CHECK (price_cents >= 0),
    image_url   TEXT        NOT NULL,
    stock       INTEGER     NOT NULL DEFAULT 0 CHECK (stock >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE shop.coupons (
    code            TEXT        PRIMARY KEY,
    kind            TEXT        NOT NULL CHECK (kind IN ('percent', 'fixed')),
    value           BIGINT      NOT NULL CHECK (value > 0),
    min_total_cents BIGINT      NOT NULL DEFAULT 0 CHECK (min_total_cents >= 0),
    expires_at      TIMESTAMPTZ,
    only_for        JSONB,
    CHECK (kind <> 'percent' OR value <= 100)
);

CREATE TABLE shop.carts (
    id          BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     UUID        NOT NULL UNIQUE,
    coupon_code TEXT        REFERENCES shop.coupons (code) ON DELETE SET NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX carts_coupon_code_idx ON shop.carts (coupon_code);

CREATE TABLE shop.cart_items (
    cart_id    BIGINT  NOT NULL REFERENCES shop.carts (id) ON DELETE CASCADE,
    product_id BIGINT  NOT NULL REFERENCES shop.products (id),
    quantity   INTEGER NOT NULL,
    PRIMARY KEY (cart_id, product_id)
);

CREATE INDEX cart_items_product_id_idx ON shop.cart_items (product_id);

CREATE TABLE shop.orders (
    id             BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id        UUID        NOT NULL,
    status         TEXT        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'failed')),
    currency       TEXT        NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    subtotal_cents BIGINT      NOT NULL CHECK (subtotal_cents >= 0),
    discount_cents BIGINT      NOT NULL CHECK (discount_cents >= 0),
    total_cents    BIGINT      NOT NULL CHECK (total_cents >= 0),
    coupon_code    TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX orders_user_id_created_at_idx ON shop.orders (user_id, created_at DESC, id DESC);
CREATE INDEX orders_created_at_idx ON shop.orders (created_at);

CREATE TABLE shop.order_items (
    order_id         BIGINT  NOT NULL REFERENCES shop.orders (id) ON DELETE CASCADE,
    product_id       BIGINT  NOT NULL REFERENCES shop.products (id),
    name             TEXT    NOT NULL,
    unit_price_cents BIGINT  NOT NULL,
    quantity         INTEGER NOT NULL,
    PRIMARY KEY (order_id, product_id)
);

CREATE INDEX order_items_product_id_idx ON shop.order_items (product_id);

-- +goose Down
DROP TABLE shop.order_items;
DROP TABLE shop.orders;
DROP TABLE shop.cart_items;
DROP TABLE shop.carts;
DROP TABLE shop.coupons;
DROP TABLE shop.products;
