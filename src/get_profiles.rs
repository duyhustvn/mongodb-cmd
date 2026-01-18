use actix_web::{HttpResponse, Responder, get, web};
use mongodb::{
    Collection,
    bson::{Document, doc},
    options::ClientOptions,
};
use serde::{Deserialize, Serialize};

use crate::config::MongodbConfig;

#[derive(Deserialize)]
struct CountSystemProfileRequest {
    databases: Option<String>,
}

#[derive(Serialize)]
struct CountSystemProfileResponse {
    results: Vec<CountSystemProfileResult>,
}

#[derive(Serialize)]
struct CountSystemProfileResult {
    url: String,
    database: String,
    count: u64,
    // only include error field if it exists
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

#[get("/mongodb-cmd/profiles")]
async fn get_profiles(query: web::Query<CountSystemProfileRequest>) -> impl Responder {
    let mongodb_config = MongodbConfig::new();

    let mut results = Vec::new();

    let mut search_dbs: Vec<String> = Vec::new();
    if let Some(search_dbs_str) = &query.databases {
        search_dbs = search_dbs_str.split(',').map(|s| s.to_string()).collect();
    }

    for url in mongodb_config.url {
        let options = ClientOptions::parse(&url)
            .await
            .expect("Failed to parse Mongodb url");
        let client = mongodb::Client::with_options(options).expect("Failed to init MongoDB client");

        let parsed_url = url::Url::parse(&url).expect("Invalid URL");

        let host = parsed_url
            .host_str()
            .expect("Url must have a host")
            .to_string();

        if search_dbs.len() == 0 {
            match client.list_database_names().await {
                Ok(db_result) => search_dbs = db_result,
                Err(err) => {
                    println!("Error while list database: {}", err);
                    results.push(CountSystemProfileResult {
                        url: host.clone(),
                        database: String::new(),
                        count: 0,
                        error: Some(err.to_string()),
                    });
                    return HttpResponse::InternalServerError()
                        .json(CountSystemProfileResponse { results });
                }
            }
        }

        let collection_name = "system.profile";

        for db_name in &search_dbs {
            let db = client.database(db_name.as_str());
            let profiles: Collection<Document> = db.collection(collection_name);

            match profiles.count_documents(doc! {}).await {
                Ok(count) => {
                    results.push(CountSystemProfileResult {
                        url: host.clone(),
                        database: db_name.to_string(),
                        count,
                        error: None,
                    });
                }
                Err(err) => {
                    results.push(CountSystemProfileResult {
                        url: host.clone(),
                        database: db_name.to_string(),
                        count: 0,
                        error: Some(err.to_string()),
                    });
                }
            };
        }
    }

    HttpResponse::Ok().json(CountSystemProfileResponse { results })
}
