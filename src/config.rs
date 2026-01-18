use anyhow::Result;
use std::env;

pub struct MongodbConfig {
    pub url: Vec<String>,
}

impl MongodbConfig {
    pub fn new() -> MongodbConfig {
        let mongodb_url = Self::get_mongodb_datasource().unwrap_or_else(|err| {
            eprintln!("Error: {}", err);
            std::process::exit(1);
        });

        MongodbConfig { url: mongodb_url }
    }

    fn get_mongodb_datasource() -> Result<Vec<String>, String> {
        let host = match env::var("MONGODB_HOST") {
            Ok(val) if !val.trim().is_empty() => val,
            Ok(_) => return Err("MONGODB_URL is set but empty".to_string()),
            Err(_) => return Err("MONGODB_URL is not set".to_string()),
        };

        let username = match env::var("MONGODB_USERNAME") {
            Ok(val) if !val.trim().is_empty() => val,
            Ok(_) => return Err("MONGODB_USERNAME is set but empty".to_string()),
            Err(_) => return Err("MONGODB_USERNAME is not set".to_string()),
        };

        let password = match env::var("MONGODB_PASSWORD") {
            Ok(val) if !val.trim().is_empty() => val,
            Ok(_) => return Err("MONGODB_USERNAME is set but empty".to_string()),
            Err(_) => return Err("MONGODB_USERNAME is not set".to_string()),
        };

        let mut urls = Vec::new();

        for part in host.split(",") {
            let trimmed = part.trim();

            if trimmed.is_empty() {
                continue;
            }

            let url = format!(
                "mongodb://{}:{}@{}/?directConnection=true",
                username, password, trimmed
            );
            urls.push(url);
        }

        Ok(urls)
    }
}
