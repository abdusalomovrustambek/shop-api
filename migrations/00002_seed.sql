-- +goose Up
INSERT INTO shop.products (sku, name, description, price_cents, image_url, stock) VALUES
    ('SH-001', 'Aurora Wireless Headphones', 'Over-ear Bluetooth headphones with 30 hours of battery.', 12900, '/img/products/sh-001.jpg', 4000),
    ('SH-002', 'Pulse Earbuds', 'True wireless earbuds with a charging case.', 7900, '/img/products/sh-002.jpg', 4000),
    ('SH-003', 'Nimbus Smart Speaker', 'Room-filling speaker with a built-in voice assistant.', 8900, '/img/products/sh-003.jpg', 3500),
    ('SH-004', 'Vertex Mechanical Keyboard', 'Tenkeyless keyboard with hot-swappable switches.', 11900, '/img/products/sh-004.jpg', 3500),
    ('SH-005', 'Glide Wireless Mouse', 'Ergonomic mouse with a quiet click and USB-C charging.', 3900, '/img/products/sh-005.jpg', 5000),
    ('SH-006', 'Lumen Desk Lamp', 'LED desk lamp with five brightness levels.', 4500, '/img/products/sh-006.jpg', 3000),
    ('SH-007', 'Orbit 27 Monitor', '27-inch QHD monitor with a thin bezel.', 29900, '/img/products/sh-007.jpg', 2000),
    ('SH-008', 'Stride Laptop Stand', 'Foldable aluminium stand for laptops up to 17 inches.', 3500, '/img/products/sh-008.jpg', 4500),
    ('SH-009', 'Anchor USB-C Hub', 'Seven-port hub with HDMI, card reader and 100 W pass-through.', 5900, '/img/products/sh-009.jpg', 4000),
    ('SH-010', 'Volt 65W Charger', 'Compact GaN charger with two USB-C ports.', 4900, '/img/products/sh-010.jpg', 5000),
    ('SH-011', 'Tether Braided Cable', 'One metre USB-C cable, braided nylon.', 1200, '/img/products/sh-011.jpg', 5000),
    ('SH-012', 'Shield Phone Case', 'Slim shock-absorbing case.', 1900, '/img/products/sh-012.jpg', 5000),
    ('SH-013', 'Beacon Power Bank', '20000 mAh power bank with fast charging.', 4400, '/img/products/sh-013.jpg', 3500),
    ('SH-014', 'Frame Webcam', 'Full HD webcam with a privacy shutter.', 5400, '/img/products/sh-014.jpg', 3000),
    ('SH-015', 'Echo Studio Microphone', 'USB condenser microphone for calls and podcasts.', 8400, '/img/products/sh-015.jpg', 2500),
    ('SH-016', 'Ember Ceramic Mug', 'Self-heating mug that keeps drinks at 55 degrees.', 6900, '/img/products/sh-016.jpg', 3000),
    ('SH-017', 'Brew French Press', 'One litre borosilicate glass French press.', 2900, '/img/products/sh-017.jpg', 3500),
    ('SH-018', 'Ground Coffee Grinder', 'Burr grinder with 18 grind settings.', 5900, '/img/products/sh-018.jpg', 2500),
    ('SH-019', 'Kettle Pro', 'Variable temperature gooseneck kettle.', 6400, '/img/products/sh-019.jpg', 2500),
    ('SH-020', 'Pour-Over Set', 'Dripper, server and 100 filters.', 3400, '/img/products/sh-020.jpg', 3000),
    ('SH-021', 'Harvest Cutting Board', 'End-grain walnut board, 40 by 30 cm.', 4800, '/img/products/sh-021.jpg', 2500),
    ('SH-022', 'Edge Chef Knife', '20 cm stainless steel chef knife.', 7400, '/img/products/sh-022.jpg', 2500),
    ('SH-023', 'Sear Cast-Iron Pan', 'Pre-seasoned 26 cm skillet.', 5400, '/img/products/sh-023.jpg', 2500),
    ('SH-024', 'Bento Lunch Box', 'Leak-proof two-compartment lunch box.', 2200, '/img/products/sh-024.jpg', 4000),
    ('SH-025', 'Trail Water Bottle', 'Insulated 750 ml steel bottle.', 2600, '/img/products/sh-025.jpg', 4500),
    ('SH-026', 'Summit Daypack', '22-litre backpack with a laptop sleeve.', 6900, '/img/products/sh-026.jpg', 3000),
    ('SH-027', 'Roam Travel Organizer', 'Cable and charger organizer for trips.', 1800, '/img/products/sh-027.jpg', 4500),
    ('SH-028', 'Drift Yoga Mat', 'Non-slip 6 mm mat with a carry strap.', 3200, '/img/products/sh-028.jpg', 3500),
    ('SH-029', 'Flex Resistance Bands', 'Set of five bands with a door anchor.', 2000, '/img/products/sh-029.jpg', 4000),
    ('SH-030', 'Pace Fitness Tracker', 'Heart-rate and sleep tracking, 10-day battery.', 5900, '/img/products/sh-030.jpg', 3000),
    ('SH-031', 'Haven Throw Blanket', 'Soft knitted blanket, 130 by 170 cm.', 3900, '/img/products/sh-031.jpg', 3000),
    ('SH-032', 'Glow Scented Candle', 'Soy wax candle with a 40-hour burn time.', 1600, '/img/products/sh-032.jpg', 4500),
    ('SH-033', 'Sprout Desk Plant', 'Low-light plant in a ceramic pot.', 2400, '/img/products/sh-033.jpg', 2000),
    ('SH-034', 'Canvas Wall Print', 'Abstract print on canvas, 50 by 70 cm.', 4900, '/img/products/sh-034.jpg', 2000),
    ('SH-035', 'Slate Notebook', 'Dotted hardcover notebook, 192 pages.', 1400, '/img/products/sh-035.jpg', 5000),
    ('SH-036', 'Inkwell Gel Pens', 'Pack of ten 0.5 mm gel pens.', 900, '/img/products/sh-036.jpg', 5000),
    ('SH-037', 'Sketch Pencil Tin', 'Twenty-four colour pencils in a tin.', 1900, '/img/products/sh-037.jpg', 4000),
    ('SH-038', 'Puzzle 1000', 'Thousand-piece landscape jigsaw puzzle.', 2200, '/img/products/sh-038.jpg', 3000),
    ('SH-039', 'Deck Travel Cards', 'Waterproof playing cards.', 800, '/img/products/sh-039.jpg', 5000),
    ('SH-040', 'Spark Lighter', 'Rechargeable arc lighter.', 1500, '/img/products/sh-040.jpg', 4000);

INSERT INTO shop.coupons (code, kind, value, min_total_cents, expires_at, only_for) VALUES
    ('WELCOME10', 'percent', 10, 0, NULL, NULL),
    ('BLACKFRIDAY', 'percent', 20, 5000, '2099-01-01T00:00:00Z', NULL),
    ('FREESHIP', 'fixed', 500, 3000, NULL, NULL),
    ('VIP25', 'percent', 25, 0, NULL, '["SH-001", "SH-002", "SH-003", "SH-004", "SH-005"]'),
    ('EXPIRED5', 'percent', 5, 0, '2020-01-01T00:00:00Z', NULL),
    ('TEAM50', 'fixed', 5000, 0, NULL, NULL);

-- +goose Down
DELETE FROM shop.coupons WHERE code IN ('WELCOME10', 'BLACKFRIDAY', 'FREESHIP', 'VIP25', 'EXPIRED5', 'TEAM50');
DELETE FROM shop.products WHERE sku LIKE 'SH-0%';
