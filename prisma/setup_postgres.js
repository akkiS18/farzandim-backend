const { Client } = require('pg');
const fs = require('fs');
const path = require('path');

const pgConfig = {
    user: 'postgres',
    password: 'postgres',
    host: 'localhost',
    port: 5432,
};

async function main() {
    console.log("Connecting to default postgres database to check for 'farzandim'...");
    
    // Connect to postgres default DB to check / create central DB
    const rootClient = new Client({
        ...pgConfig,
        database: 'postgres',
    });
    
    try {
        await rootClient.connect();
        
        const res = await rootClient.query("SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = 'farzandim')");
        const exists = res.rows[0].exists;
        
        if (!exists) {
            console.log("Database 'farzandim' does not exist. Creating it...");
            await rootClient.query("CREATE DATABASE farzandim");
            console.log("Database 'farzandim' created successfully.");
        } else {
            console.log("Database 'farzandim' already exists.");
        }
    } catch (err) {
        console.error("Error checking/creating 'farzandim' database:", err);
        process.exit(1);
    } finally {
        await rootClient.end();
    }
    
    console.log("Connecting to 'farzandim' database to apply central migrations...");
    const centralClient = new Client({
        ...pgConfig,
        database: 'farzandim',
    });
    
    try {
        await centralClient.connect();
        
        // Read central DB SQL migration (paths are relative to this script's directory)
        const migrationPath = path.join(__dirname, '..', 'migrations', 'central', '000001_init_central.up.sql');
        console.log(`Reading SQL migration from: ${migrationPath}`);
        const sql = fs.readFileSync(migrationPath, 'utf8');
        
        await centralClient.query(sql);
        console.log("Central DB migrations applied successfully to 'farzandim' database.");
    } catch (err) {
        console.error("Error applying migrations to 'farzandim':", err);
        process.exit(1);
    } finally {
        await centralClient.end();
    }
}

main();
