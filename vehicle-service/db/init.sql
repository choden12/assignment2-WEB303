CREATE TABLE IF NOT EXISTS vehicles (
    id SERIAL PRIMARY KEY,
    registration_number VARCHAR(50) NOT NULL UNIQUE,
    owner_name VARCHAR(100) NOT NULL,
    model VARCHAR(100) NOT NULL,
    year INTEGER NOT NULL
);